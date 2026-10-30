package networks

import (
	"context"
	"net/netip"
	"slices"
	"strings"
)

// Topology is every network an instance can be on, and what is on each
// (#128): the host's bridges, the vm-bhyve switches on them, and the jails'
// epairs and guests' taps that are their members. Read-only, and read off the
// host at every request (JD-011).
type Topology struct {
	Bridges []Bridge `json:"bridges"`
}

// Kinds of bridge, by whose it is.
const (
	// KindDefault is the default VM network's switch (JD-040).
	KindDefault = "default"
	// KindTenant is a tenant network's bridge, jdn<i> (JD-043).
	KindTenant = "tenant"
	// KindOperator is anything else: a switch or bridge the operator made.
	KindOperator = "operator"
)

// NAT says whether Jail Deck's anchor translates a bridge's subnet. Only that
// anchor is read: NAT the operator wrote into pf.conf themselves is not seen.
const (
	NATLoaded    = "loaded"
	NATNotLoaded = "not-loaded"
	// NATUnknown is PF not enabled or its rules unreadable, or a bridge
	// without an address, which has no subnet to translate.
	NATUnknown = "unknown"
)

// Bridge is one bridge, or one vm-bhyve switch whose bridge is not there.
type Bridge struct {
	// Name is the bridge's interface, empty for a switch whose bridge does
	// not exist yet: vm-bhyve makes a standard switch's bridge at vm init, at
	// boot, and lists the switch with no interface until then (measured).
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
	Kind   string `json:"kind"`
	// Switch is the vm-bhyve switch on it, and SwitchType its type; empty
	// for a bridge no switch uses, as a VNET jail's may be.
	Switch     string `json:"switch,omitempty"`
	SwitchType string `json:"switch_type,omitempty"`
	// Network and Tenant are a tenant network's, from its description.
	Network     string   `json:"network,omitempty"`
	Tenant      string   `json:"tenant,omitempty"`
	Description string   `json:"description,omitempty"`
	Addresses   []string `json:"addresses"`
	NAT         string   `json:"nat"`
	Members     []Member `json:"members"`
	// InUse are the addresses of a Jail Deck network's subnet that its
	// instances have, and by which (JD-047). Jail Deck's networks only: what
	// is on an operator's is the operator's to address.
	InUse []Use `json:"in_use,omitempty"`
}

// What a member is.
const (
	MemberJail = "jail"
	MemberVM   = "vm"
	// MemberOther is an interface Jail Deck cannot attribute to an instance:
	// a physical port, a VLAN, an epair someone made by hand.
	MemberOther = "other"
)

// Member is one interface on a bridge, attributed to its instance by the
// description Jail Deck's and vm-bhyve's own start wrote on it: jail:<name>
// on a VNET jail's host epair (JD-030), vmnet/<guest>/<n>/<switch> on a
// guest's tap (JD-035).
type Member struct {
	Interface string `json:"interface"`
	Kind      string `json:"kind"`
	// Instance is the jail's or the guest's name; NIC the guest's NIC number.
	Instance string `json:"instance,omitempty"`
	NIC      string `json:"nic,omitempty"`
	// Private is the bridge's private flag on this port: private ports
	// reach the host and not each other.
	Private bool `json:"private"`
}

// Topology reads every interface and every switch once each, and the loaded
// NAT rules of Jail Deck's anchor when PF is enabled.
func (s *Service) Topology(ctx context.Context) (Topology, error) {
	if s.system == nil {
		return Topology{}, s.unavailable()
	}
	interfaces, err := s.system.Interfaces(ctx)
	if err != nil {
		return Topology{}, readError("the host's interfaces", err)
	}
	// A host without vm-bhyve still has bridges: its VNET jails'.
	switches, _ := s.system.Switches(ctx)

	var natRules []string
	natKnown := false
	if pf, err := s.system.PF(ctx); err == nil && pf.Enabled {
		if rules, err := s.system.AnchorRules(ctx, Anchor); err == nil {
			natRules, natKnown = rules, true
		}
	}

	byName := make(map[string]Interface, len(interfaces))
	for _, i := range interfaces {
		byName[i.Name] = i
	}
	switchOn := make(map[string]Switch, len(switches))
	for _, sw := range switches {
		if sw.Bridge != "" {
			switchOn[sw.Bridge] = sw
		}
	}

	topology := Topology{Bridges: make([]Bridge, 0)}
	for _, i := range interfaces {
		if !slices.Contains(i.Groups, "bridge") {
			continue
		}
		b := Bridge{Name: i.Name, Exists: true, Kind: KindOperator, Description: i.Description,
			Addresses: make([]string, 0, len(i.Addresses)), Members: make([]Member, 0, len(i.Members))}
		for _, a := range i.Addresses {
			b.Addresses = append(b.Addresses, a.String())
		}
		if sw, ok := switchOn[i.Name]; ok {
			b.Switch, b.SwitchType = sw.Name, sw.Type
			if sw.Name == s.settings.Switch {
				b.Kind = KindDefault
			}
		}
		if name, tenant, ok := parseDescription(i.Description); ok && strings.HasPrefix(i.Name, bridgePrefix) {
			b.Kind, b.Network, b.Tenant = KindTenant, name, tenant
		}
		b.NAT = natState(natKnown, natRules, i.Addresses)
		for _, m := range i.Members {
			b.Members = append(b.Members, attribute(m, byName[m.Name].Description))
		}
		if b.Kind != KindOperator && len(i.Addresses) > 0 {
			a := i.Addresses[0]
			if uses := s.addressesOf(ctx, Network{Name: b.Network, Bridge: i.Name,
				Subnet: a.Masked().String(), Gateway: a.Addr().String()}); len(uses) > 0 {
				b.InUse = uses
			}
		}
		topology.Bridges = append(topology.Bridges, b)
	}
	for _, sw := range switches {
		if sw.Bridge != "" && byName[sw.Bridge].Name != "" {
			continue
		}
		b := Bridge{Name: sw.Bridge, Kind: KindOperator, Switch: sw.Name, SwitchType: sw.Type,
			Addresses: make([]string, 0), NAT: NATUnknown, Members: make([]Member, 0)}
		if sw.Name == s.settings.Switch {
			b.Kind = KindDefault
		}
		if sw.Address != "" {
			b.Addresses = append(b.Addresses, sw.Address)
		}
		topology.Bridges = append(topology.Bridges, b)
	}
	return topology, nil
}

func natState(known bool, rules []string, addresses []netip.Prefix) string {
	if !known || len(addresses) == 0 {
		return NATUnknown
	}
	for _, a := range addresses {
		from := "from " + a.Masked().String() + " "
		for _, r := range rules {
			if strings.HasPrefix(r, "nat ") && strings.Contains(r, from) {
				return NATLoaded
			}
		}
	}
	return NATNotLoaded
}

// attribute names a member's instance from its interface's description.
func attribute(m BridgeMember, description string) Member {
	member := Member{Interface: m.Name, Kind: MemberOther, Private: m.Private}
	if name, ok := strings.CutPrefix(description, "jail:"); ok && name != "" {
		member.Kind, member.Instance = MemberJail, name
		return member
	}
	if rest, ok := strings.CutPrefix(description, "vmnet/"); ok {
		if parts := strings.Split(rest, "/"); len(parts) == 3 && parts[0] != "" {
			member.Kind, member.Instance, member.NIC = MemberVM, parts[0], parts[1]
		}
	}
	return member
}
