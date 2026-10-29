package networks

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

// The capture's shape (capture-topology-induced.sh): a guest's tap on the
// private default switch, a jail's epair on the operator's switch, a tenant
// network's bridge, a VNET jail's own bridge with no switch, and a switch
// whose bridge is not there yet.
func topologyHost() *host {
	h := freshHost()
	h.switches = []Switch{
		{Name: "vmlan", Type: "standard", Bridge: "vm-vmlan", Address: "10.80.0.1/24"},
		{Name: "jaildeck", Type: "standard", Bridge: "vm-jaildeck", Address: "10.88.0.1/24", Private: true},
		{Name: "acme-prod", Type: "manual", Bridge: "jdn3"},
		{Name: "jdcap128", Type: "standard"},
	}
	h.interfaces = []Interface{
		{Name: "em0", Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}},
		{Name: "vm-vmlan", Groups: []string{"bridge", "vm-switch"}, Addresses: []netip.Prefix{netip.MustParsePrefix("10.80.0.1/24")},
			Members: []BridgeMember{{Name: "epair0a"}}},
		{Name: "vm-jaildeck", Groups: []string{"bridge", "vm-switch"}, Addresses: []netip.Prefix{netip.MustParsePrefix("10.88.0.1/24")},
			Members: []BridgeMember{{Name: "tap0", Private: true}}},
		{Name: "tap0", Description: "vmnet/jd-cap-128/0/jaildeck", Groups: []string{"tap", "vm-port"}},
		{Name: "epair0a", Description: "jail:jd-cap-128", Groups: []string{"epair"}},
		{Name: "jdn3", Description: "jaildeck:network=acme-prod;tenant=acme", Groups: []string{"bridge"},
			Addresses: []netip.Prefix{netip.MustParsePrefix("10.92.3.1/24")}, Members: []BridgeMember{{Name: "tap1"}, {Name: "em1"}}},
		{Name: "tap1", Description: "vmnet/acme-web/1/acme-prod", Groups: []string{"tap"}},
		{Name: "bridge0", Groups: []string{"bridge"}, Members: []BridgeMember{{Name: "epair1a"}}},
		{Name: "epair1a", Description: "jail:db1"},
	}
	h.pf = PF{Enabled: true}
	h.anchor = []string{"nat on em0 inet from 10.88.0.0/24 to any -> (em0) round-robin",
		"nat on em0 inet from 10.92.3.0/24 to any -> (em0) round-robin"}
	return h
}

func TestTopology(t *testing.T) {
	s := NewService(topologyHost(), nil, Settings{Switch: "jaildeck"})
	got, err := s.Topology(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Bridge{
		{Name: "vm-vmlan", Exists: true, Kind: KindOperator, Switch: "vmlan", SwitchType: "standard",
			Addresses: []string{"10.80.0.1/24"}, NAT: NATNotLoaded,
			Members: []Member{{Interface: "epair0a", Kind: MemberJail, Instance: "jd-cap-128"}}},
		{Name: "vm-jaildeck", Exists: true, Kind: KindDefault, Switch: "jaildeck", SwitchType: "standard",
			Addresses: []string{"10.88.0.1/24"}, NAT: NATLoaded,
			Members: []Member{{Interface: "tap0", Kind: MemberVM, Instance: "jd-cap-128", NIC: "0", Private: true}}},
		{Name: "jdn3", Exists: true, Kind: KindTenant, Switch: "acme-prod", SwitchType: "manual",
			Network: "acme-prod", Tenant: "acme", Description: "jaildeck:network=acme-prod;tenant=acme",
			Addresses: []string{"10.92.3.1/24"}, NAT: NATLoaded,
			Members: []Member{{Interface: "tap1", Kind: MemberVM, Instance: "acme-web", NIC: "1"},
				{Interface: "em1", Kind: MemberOther}}},
		{Name: "bridge0", Exists: true, Kind: KindOperator, Addresses: []string{}, NAT: NATUnknown,
			Members: []Member{{Interface: "epair1a", Kind: MemberJail, Instance: "db1"}}},
		{Name: "", Exists: false, Kind: KindOperator, Switch: "jdcap128", SwitchType: "standard",
			Addresses: []string{}, NAT: NATUnknown, Members: []Member{}},
	}
	if !reflect.DeepEqual(got.Bridges, want) {
		t.Errorf("bridges =\n%+v\nwant\n%+v", got.Bridges, want)
	}
}

// With PF off, nothing translates anything Jail Deck could tell of.
func TestTopologyWithoutPFKnowsNoNAT(t *testing.T) {
	h := topologyHost()
	h.pf = PF{}
	got, err := NewService(h, nil, Settings{Switch: "jaildeck"}).Topology(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range got.Bridges {
		if b.NAT != NATUnknown {
			t.Errorf("%s nat = %s with PF off", b.Name, b.NAT)
		}
	}
}

// A host without vm-bhyve still has its VNET jails' bridges.
func TestTopologyWithoutVMBhyve(t *testing.T) {
	h := topologyHost()
	h.switches = nil
	got, err := NewService(h, nil, Settings{Switch: "jaildeck"}).Topology(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bridges) != 4 || got.Bridges[1].Kind != KindOperator || got.Bridges[1].Switch != "" {
		t.Errorf("bridges = %+v", got.Bridges)
	}
}

func TestTopologyCannotReadInterfaces(t *testing.T) {
	h := topologyHost()
	h.fail["interfaces"] = errors.New("ifconfig: boom")
	if _, err := NewService(h, nil, Settings{Switch: "jaildeck"}).Topology(context.Background()); err == nil {
		t.Error("no error")
	}
}

func TestAttribute(t *testing.T) {
	for description, want := range map[string]Member{
		"jail:web":       {Kind: MemberJail, Instance: "web"},
		"jail:":          {Kind: MemberOther},
		"vmnet/web/2/sw": {Kind: MemberVM, Instance: "web", NIC: "2"},
		"vmnet/web/2":    {Kind: MemberOther},
		"vmnet//0/sw":    {Kind: MemberOther},
		"someone's own":  {Kind: MemberOther},
		"":               {Kind: MemberOther},
	} {
		want.Interface = "x0"
		if got := attribute(BridgeMember{Name: "x0"}, description); got != want {
			t.Errorf("%q: %+v, want %+v", description, got, want)
		}
	}
}
