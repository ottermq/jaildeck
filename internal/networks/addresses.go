package networks

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"sync"

	"github.com/ottermq/jaildeck/internal/common"
)

// Own is one of the networks Jail Deck made, as an instance joins it: the
// default network (JD-040) or a tenant network (JD-043). On these, and only
// these, Jail Deck gives an instance its address (#118 step 3, JD-047). An
// operator's bridge or switch is not one: what is on it is the operator's to
// address.
type Own struct {
	// Name is "default" or the tenant network's name.
	Name string
	// Bridge is what a VNET jail's epair joins, Switch what a guest's NIC is
	// put on.
	Bridge  string
	Switch  string
	Subnet  netip.Prefix
	Gateway netip.Addr
}

// Ref names a network the way a request does: by its name, by the bridge a
// jail names, or by the switch a guest names. Exactly one is set.
type Ref struct {
	Name   string
	Bridge string
	Switch string
}

// Use is an address of a network's subnet that something has (JD-047).
type Use struct {
	Address string `json:"address"`
	// Kind is "jail" or "guest" for an address a configuration names, and
	// "seen" for one only the bridge's ARP table has: an instance that set
	// its own address, or something that is not Jail Deck's.
	Kind string `json:"kind"`
	// Name is the jail's or guest's, for a jail or a guest.
	Name string `json:"name,omitempty"`
}

// The kinds of Use.
const (
	UseJail  = "jail"
	UseGuest = "guest"
	UseSeen  = "seen"
)

// Lease is an address Reserve picked or checked, and the gateway that goes
// with it.
type Lease struct {
	Network string
	// Address carries the subnet's prefix: what an interface is given.
	Address netip.Prefix
	Gateway netip.Addr
}

// own lists Jail Deck's networks: the default network when its switch is
// there with its address, and every tenant network that has its subnet.
func (s *Service) own(ctx context.Context) ([]Own, error) {
	list := make([]Own, 0)
	if p, err := s.prefix(); err == nil {
		switches, err := s.system.Switches(ctx)
		if err != nil {
			return nil, readError("vm-bhyve's switches", err)
		}
		for _, sw := range switches {
			if sw.Name == s.settings.Switch && sw.Address == p.String() {
				list = append(list, Own{Name: "default", Bridge: sw.Bridge, Switch: sw.Name,
					Subnet: p.Masked(), Gateway: p.Addr()})
			}
		}
	}
	tenants, err := s.tenantNetworks(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tenants {
		subnet, err := netip.ParsePrefix(t.Subnet)
		if err != nil {
			continue
		}
		gateway, err := netip.ParseAddr(t.Gateway)
		if err != nil {
			continue
		}
		list = append(list, Own{Name: t.Name, Bridge: t.Bridge, Switch: t.Name, Subnet: subnet, Gateway: gateway})
	}
	return list, nil
}

// Find is the network ref names. ok is false for a bridge or a switch that is
// not one of Jail Deck's; a name that is not one is a 400, since a name can
// only mean one of them.
func (s *Service) Find(ctx context.Context, ref Ref) (Own, bool, error) {
	if s.system == nil {
		return Own{}, false, s.unavailable()
	}
	list, err := s.own(ctx)
	if err != nil {
		return Own{}, false, err
	}
	for _, n := range list {
		switch {
		case ref.Name != "" && n.Name == ref.Name,
			ref.Bridge != "" && n.Bridge == ref.Bridge,
			ref.Switch != "" && n.Switch == ref.Switch:
			return n, true, nil
		}
	}
	if ref.Name != "" {
		names := make([]string, 0, len(list))
		for _, n := range list {
			names = append(names, n.Name)
		}
		have := "Jail Deck has no network set up"
		if len(names) > 0 {
			have = "it has " + joinQuoted(names)
		}
		return Own{}, false, common.NewAppError(http.StatusBadRequest,
			fmt.Sprintf("there is no network named %s; %s", strconv.Quote(ref.Name), have))
	}
	return Own{}, false, nil
}

// Uses is every address of the network's subnet that something has: an
// address in a jail's configuration or a guest's seed, and what the bridge's
// ARP table has seen. The gateway is the host's, and is left out.
func (s *Service) Uses(ctx context.Context, n Own) ([]Use, error) {
	found, err := s.system.AddressUses(ctx, n.Subnet, n.Bridge)
	if err != nil {
		return nil, readError("the addresses in use on "+n.Bridge, err)
	}
	// One row per address: a configuration says whose it is, and an ARP
	// entry for the same address says nothing more.
	byAddress := map[string]Use{}
	for _, u := range found {
		addr, err := netip.ParseAddr(u.Address)
		if err != nil || !n.Subnet.Contains(addr) || addr == n.Gateway {
			continue
		}
		if have, ok := byAddress[u.Address]; ok && (have.Kind != UseSeen || u.Kind == UseSeen) {
			continue
		}
		byAddress[u.Address] = u
	}
	uses := make([]Use, 0, len(byAddress))
	for _, u := range byAddress {
		uses = append(uses, u)
	}
	slices.SortFunc(uses, func(a, b Use) int {
		return netip.MustParseAddr(a.Address).Compare(netip.MustParseAddr(b.Address))
	})
	return uses, nil
}

// Reserve gives an instance joining n its address: want when it is a free
// address of the subnet, or the lowest free one when want is not set. Until
// release is called, no other Reserve on n runs, so two creates cannot be
// handed one address: the caller releases once the address is written into
// the jail's stanza or the guest's seed, where the next Reserve reads it.
// Anything that cannot be read refuses, rather than guesses.
func (s *Service) Reserve(ctx context.Context, n Own, want netip.Addr) (Lease, func(), error) {
	if want.IsValid() {
		if err := checkWanted(n, want); err != nil {
			return Lease{}, nil, err
		}
	}
	mu := s.networkLock(n.Name)
	mu.Lock()
	release := sync.OnceFunc(mu.Unlock)
	fail := func(err error) (Lease, func(), error) {
		release()
		return Lease{}, nil, err
	}
	uses, err := s.Uses(ctx, n)
	if err != nil {
		return fail(err)
	}
	taken := map[netip.Addr]Use{}
	for _, u := range uses {
		taken[netip.MustParseAddr(u.Address)] = u
	}
	lease := Lease{Network: n.Name, Gateway: n.Gateway}
	if want.IsValid() {
		if u, ok := taken[want]; ok {
			return fail(common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s on the network %s is in use by %s", want, strconv.Quote(n.Name), describeUse(u))))
		}
		lease.Address = netip.PrefixFrom(want, n.Subnet.Bits())
		return lease, release, nil
	}
	last := lastAddress(n.Subnet)
	for a := n.Subnet.Addr().Next(); a.Less(last); a = a.Next() {
		if _, ok := taken[a]; ok || a == n.Gateway {
			continue
		}
		lease.Address = netip.PrefixFrom(a, n.Subnet.Bits())
		return lease, release, nil
	}
	return fail(common.NewAppError(http.StatusConflict, fmt.Sprintf(
		"the network %s has no free address left in %s", strconv.Quote(n.Name), n.Subnet)))
}

// checkWanted refuses an address an instance cannot have on n: one outside
// its subnet, the subnet's own or its broadcast address, or the gateway.
func checkWanted(n Own, want netip.Addr) error {
	bad := func(why string) error {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%s cannot be an address on the network %s: %s", want, strconv.Quote(n.Name), why))
	}
	switch {
	case !n.Subnet.Contains(want):
		return bad("it is outside " + n.Subnet.String())
	case want == n.Subnet.Addr():
		return bad("it is the subnet's own address")
	case want == lastAddress(n.Subnet):
		return bad("it is the subnet's broadcast address")
	case want == n.Gateway:
		return bad("it is the gateway, the host's")
	}
	return nil
}

// lastAddress is an IPv4 subnet's broadcast address.
func lastAddress(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	n := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	n |= 1<<(32-p.Bits()) - 1
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

func describeUse(u Use) string {
	if u.Kind == UseSeen {
		return "something the bridge has seen (its ARP table), which no jail or guest configuration names"
	}
	return u.Kind + " " + u.Name
}

// networkLock is the one lock of the network named name.
func (s *Service) networkLock(name string) *sync.Mutex {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if s.leases == nil {
		s.leases = map[string]*sync.Mutex{}
	}
	mu, ok := s.leases[name]
	if !ok {
		mu = &sync.Mutex{}
		s.leases[name] = mu
	}
	return mu
}

func joinQuoted(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return joinList(quoted)
}

func joinList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	out := items[0]
	for _, it := range items[1 : len(items)-1] {
		out += ", " + it
	}
	return out + " and " + items[len(items)-1]
}
