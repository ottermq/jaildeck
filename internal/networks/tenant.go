package networks

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// Network is a tenant network (JD-043): a bridge Jail Deck owns, the host's
// address on it as the gateway, and a vm-bhyve manual switch on top, kept
// apart from every other VM network by the rules in the jaildeck/vmnet
// anchor. Everything here is read off the host: the network's name and
// tenant are in the bridge's description, its subnet is the bridge's address.
type Network struct {
	// Name is the vm-bhyve switch's: what a guest's NIC is put on.
	Name string `json:"name"`
	// Tenant is the client's own label for whose network it is, if it gave
	// one.
	Tenant string `json:"tenant,omitempty"`
	// Bridge is the host's interface, jdn<i>: what a VNET jail's epair joins.
	Bridge  string `json:"bridge"`
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
	// Members are the interfaces on the bridge now: running guests' taps and
	// VNET jails' epairs.
	Members []string `json:"members"`
}

// NetworkList is GET /api/networks.
type NetworkList struct {
	// Range is the block tenant networks' subnets are allocated from, one
	// /24 each (JAILDECK_NETWORK_RANGE).
	Range    string    `json:"range"`
	Networks []Network `json:"networks"`
}

// CreateNetworkRequest is the body of POST /api/networks.
type CreateNetworkRequest struct {
	Name   string `json:"name"`
	Tenant string `json:"tenant,omitempty"`
}

// NetworkRemoval is what DELETE /api/networks/{name} answers.
type NetworkRemoval struct {
	Name string `json:"name"`
}

// The audit log's names for what a tenant network's create and destroy run.
const (
	OperationCreateNetworkBridge  = "create-network-bridge"
	OperationSetNetworkAddress    = "set-network-address"
	OperationDestroySwitch        = "destroy-vm-switch"
	OperationDestroyNetworkBridge = "destroy-network-bridge"
)

// bridgePrefix names a tenant network's bridge: jdn<i>, where i is the /24's
// place in the range. Bridge names are interface names, 15 characters at
// most, so the network's own name cannot be the bridge's.
const bridgePrefix = "jdn"

// descriptionPrefix marks a bridge as a tenant network's, and carries what
// the host would not otherwise know: the network's name and its tenant.
const descriptionPrefix = "jaildeck:network="

// networkName is a tenant network's name: what vm-bhyve calls the switch,
// so a guest's NIC can name it. Lowercase, digits and '-', 1 to 32.
var networkName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// tenantLabel is the client's label, kept in the bridge's description.
var tenantLabel = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func description(name, tenant string) string {
	d := descriptionPrefix + name
	if tenant != "" {
		d += ";tenant=" + tenant
	}
	return d
}

// parseDescription reads a bridge's description back: the network's name and
// tenant, and whether it is a tenant network's at all.
func parseDescription(d string) (name, tenant string, ok bool) {
	rest, found := strings.CutPrefix(d, descriptionPrefix)
	if !found {
		return "", "", false
	}
	name, tail, _ := strings.Cut(rest, ";")
	tenant, _ = strings.CutPrefix(tail, "tenant=")
	return name, tenant, networkName.MatchString(name)
}

// networkRange is the configured range, checked: IPv4, room for at least one
// /24.
func (s *Service) networkRange() (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s.settings.Range)
	if err != nil || !p.Addr().Is4() || p.Bits() > 24 || p.Bits() < 16 {
		return netip.Prefix{}, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"JAILDECK_NETWORK_RANGE is %q; it must be an IPv4 block from /16 to /24, such as 10.92.0.0/16",
			s.settings.Range))
	}
	return p.Masked(), nil
}

// subnetAt is the range's i-th /24.
func subnetAt(r netip.Prefix, i int) netip.Prefix {
	a := r.Addr().As4()
	n := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	n += uint32(i) << 8
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}), 24)
}

// Networks reads every tenant network off the host.
func (s *Service) Networks(ctx context.Context) (NetworkList, error) {
	if s.system == nil {
		return NetworkList{}, s.unavailable()
	}
	r, err := s.networkRange()
	if err != nil {
		return NetworkList{}, err
	}
	list, err := s.tenantNetworks(ctx)
	if err != nil {
		return NetworkList{}, err
	}
	return NetworkList{Range: r.String(), Networks: list}, nil
}

// Network reads one tenant network.
func (s *Service) Network(ctx context.Context, name string) (Network, error) {
	if s.system == nil {
		return Network{}, s.unavailable()
	}
	list, err := s.tenantNetworks(ctx)
	if err != nil {
		return Network{}, err
	}
	for _, n := range list {
		if n.Name == name {
			return n, nil
		}
	}
	return Network{}, common.NewAppError(http.StatusNotFound, "there is no network named "+strconv.Quote(name))
}

func (s *Service) tenantNetworks(ctx context.Context) ([]Network, error) {
	bridges, err := s.system.TenantBridges(ctx)
	if err != nil {
		return nil, readError("the tenant networks' bridges", err)
	}
	list := make([]Network, 0, len(bridges))
	for _, b := range bridges {
		name, tenant, ok := parseDescription(b.Description)
		if !ok {
			// A jdn bridge someone else made, or one a failed create left
			// half described: not a network to report or to write rules for.
			continue
		}
		n := Network{Name: name, Tenant: tenant, Bridge: b.Name, Members: b.Members}
		if b.Address.IsValid() {
			n.Subnet, n.Gateway = b.Address.Masked().String(), b.Address.Addr().String()
		}
		if n.Members == nil {
			n.Members = make([]string, 0)
		}
		list = append(list, n)
	}
	slices.SortFunc(list, func(a, b Network) int { return strings.Compare(a.Name, b.Name) })
	return list, nil
}

// CreateNetwork makes a tenant network (JD-043): a bridge with the host's
// address on a free /24 of the range, a vm-bhyve manual switch on it, and the
// anchor rewritten so that the network reaches its own gateway and the
// outside and nothing else of the host's or of another network's. A step
// that fails takes back what the earlier ones made: a network without its
// rules would be one other tenants can reach.
func (s *Service) CreateNetwork(ctx context.Context, req CreateNetworkRequest) (Network, error) {
	if s.system == nil {
		return Network{}, s.unavailable()
	}
	ctx = context.WithoutCancel(ctx)
	bad := func(format string, args ...any) (Network, error) {
		return Network{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(format, args...))
	}
	if !networkName.MatchString(req.Name) || req.Name == "default" {
		return bad("%s is not a network name: lowercase letters, digits and '-', at most 32, "+
			"beginning and ending with a letter or digit; \"default\" is the default network's", strconv.Quote(req.Name))
	}
	if req.Tenant != "" && !tenantLabel.MatchString(req.Tenant) {
		return bad("tenant is %s; give letters, digits, '.', '_' and '-', at most 64", strconv.Quote(req.Tenant))
	}
	r, err := s.networkRange()
	if err != nil {
		return Network{}, err
	}

	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()

	switches, err := s.system.Switches(ctx)
	if err != nil {
		return Network{}, err
	}
	for _, sw := range switches {
		if sw.Name == req.Name {
			return Network{}, common.NewAppError(http.StatusConflict,
				"vm-bhyve already has a switch named "+strconv.Quote(req.Name))
		}
	}
	existing, err := s.tenantNetworks(ctx)
	if err != nil {
		return Network{}, err
	}
	bridges, err := s.system.TenantBridges(ctx)
	if err != nil {
		return Network{}, readError("the tenant networks' bridges", err)
	}
	inUse, err := s.system.AddressesInUse(ctx)
	if err != nil {
		return Network{}, readError("the host's addresses", err)
	}
	index, subnet, ok := freeSubnet(r, bridges, inUse)
	if !ok {
		return Network{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s has no free /24 left: every one is a network's, or overlaps an address the host has", r))
	}
	bridge := bridgePrefix + strconv.Itoa(index)
	gateway := netip.PrefixFrom(subnet.Addr().Next(), 24)
	network := Network{Name: req.Name, Tenant: req.Tenant, Bridge: bridge, Subnet: subnet.String(),
		Gateway: gateway.Addr().String(), Members: make([]string, 0)}

	made := []func(){}
	undo := func(step string, cause error) error {
		for i := len(made) - 1; i >= 0; i-- {
			made[i]()
		}
		return operationError(step+" for the network "+strconv.Quote(req.Name), cause)
	}
	command, err := s.system.CreateTenantBridge(ctx, bridge, description(req.Name, req.Tenant))
	s.log(ctx, OperationCreateNetworkBridge, bridge, command, err)
	// Not atomic, measured: the bridge can be there with the command failed.
	made = append(made, func() {
		cmd, err := s.system.DestroyBridge(ctx, bridge)
		s.log(ctx, OperationDestroyNetworkBridge, bridge, cmd, err)
	})
	if err != nil {
		return Network{}, undo("create the bridge", err)
	}
	command, err = s.system.SetBridgeAddress(ctx, bridge, gateway.String())
	s.log(ctx, OperationSetNetworkAddress, bridge, command, err)
	if err != nil {
		return Network{}, undo("give the host its address on the bridge", err)
	}
	command, err = s.system.CreateManualSwitch(ctx, req.Name, bridge)
	s.log(ctx, OperationCreateSwitch, req.Name, command, err)
	if err != nil {
		return Network{}, undo("create the vm-bhyve switch", err)
	}
	made = append(made, func() {
		cmd, err := s.system.DestroySwitch(ctx, req.Name)
		s.log(ctx, OperationDestroySwitch, req.Name, cmd, err)
	})
	if err := s.writeRules(ctx, append(existing, network)); err != nil {
		// The file goes back to what it was before this network.
		defer func() { _ = s.writeRules(ctx, existing) }()
		return Network{}, undo("write and load the rules", err)
	}
	return network, nil
}

// freeSubnet is the lowest /24 of the range that no tenant bridge has and no
// address on the host overlaps.
func freeSubnet(r netip.Prefix, bridges []TenantBridge, inUse []netip.Prefix) (int, netip.Prefix, bool) {
	taken := map[string]bool{}
	for _, b := range bridges {
		taken[b.Name] = true
	}
	for i := 0; i < 1<<(24-r.Bits()); i++ {
		subnet := subnetAt(r, i)
		if taken[bridgePrefix+strconv.Itoa(i)] {
			continue
		}
		if slices.ContainsFunc(inUse, func(p netip.Prefix) bool { return p.Overlaps(subnet) }) {
			continue
		}
		return i, subnet, true
	}
	return 0, netip.Prefix{}, false
}

// DestroyNetwork takes a tenant network away, refused while anything is on
// it: a guest whose configuration names its switch, running or not, a jail
// whose configuration names its bridge, or any interface still a member.
// confirm is the network's name again.
func (s *Service) DestroyNetwork(ctx context.Context, name, confirm string) (NetworkRemoval, error) {
	if s.system == nil {
		return NetworkRemoval{}, s.unavailable()
	}
	ctx = context.WithoutCancel(ctx)
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()

	list, err := s.tenantNetworks(ctx)
	if err != nil {
		return NetworkRemoval{}, err
	}
	i := slices.IndexFunc(list, func(n Network) bool { return n.Name == name })
	if i < 0 {
		return NetworkRemoval{}, common.NewAppError(http.StatusNotFound, "there is no network named "+strconv.Quote(name))
	}
	network := list[i]
	if confirm != name {
		return NetworkRemoval{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"destroying a network needs confirm=%s", name))
	}
	guests, err := s.system.SwitchUsers(ctx, name)
	if err != nil {
		return NetworkRemoval{}, readError("which guests use "+name, err)
	}
	jails, err := s.system.BridgeUsers(ctx, network.Bridge)
	if err != nil {
		return NetworkRemoval{}, readError("which jails use "+network.Bridge, err)
	}
	if len(guests)+len(jails)+len(network.Members) > 0 {
		using := make([]string, 0)
		for _, g := range guests {
			using = append(using, "guest "+g)
		}
		for _, j := range jails {
			using = append(using, "jail "+j)
		}
		if len(using) == 0 {
			using = append(using, "interfaces "+strings.Join(network.Members, ", "))
		}
		return NetworkRemoval{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is in use by %s; a network is destroyed empty", strconv.Quote(name), strings.Join(using, ", ")))
	}

	command, err := s.system.DestroySwitch(ctx, name)
	s.log(ctx, OperationDestroySwitch, name, command, err)
	if err != nil {
		return NetworkRemoval{}, operationError("destroy the switch of "+strconv.Quote(name), err)
	}
	command, err = s.system.DestroyBridge(ctx, network.Bridge)
	s.log(ctx, OperationDestroyNetworkBridge, network.Bridge, command, err)
	if err != nil {
		return NetworkRemoval{}, operationError("destroy the bridge of "+strconv.Quote(name), err)
	}
	rest := slices.Delete(slices.Clone(list), i, i+1)
	if err := s.writeRules(ctx, rest); err != nil {
		return NetworkRemoval{}, operationError("rewrite the rules without "+strconv.Quote(name), err)
	}
	return NetworkRemoval{Name: name}, nil
}

// writeRules rewrites the anchor file for the default network and these
// tenant networks, and loads it if PF is up. Called with ensureMu held: the
// default network and the tenant networks share the one file.
func (s *Service) writeRules(ctx context.Context, tenants []Network) error {
	content, err := s.anchorText(ctx, tenants)
	if err != nil {
		return err
	}
	command, err := s.system.WriteAnchorFile(ctx, s.anchorFile(), content)
	s.log(ctx, OperationWriteAnchor, s.anchorFile(), command, err)
	if err != nil {
		return err
	}
	pf, err := s.system.PF(ctx)
	if err != nil || !pf.Enabled {
		// Nothing to load into; PF loads the file itself when it starts.
		return nil
	}
	command, err = s.system.LoadAnchor(ctx, Anchor, s.anchorFile())
	s.log(ctx, OperationLoadAnchor, Anchor, command, err)
	return err
}

// anchorText is the whole anchor file, from what the host has now.
func (s *Service) anchorText(ctx context.Context, tenants []Network) (string, error) {
	p, err := s.prefix()
	if err != nil {
		return "", err
	}
	egress, err := s.egress(ctx)
	if err != nil {
		return "", err
	}
	if egress == "" {
		return "", common.NewAppError(http.StatusConflict,
			"the host has no default route, so there is no interface for NAT to go out of; "+
				"set JAILDECK_VM_EGRESS or give the host a default route")
	}
	return anchorContent(p.Masked(), "vm-"+s.settings.Switch, egress, tenants), nil
}
