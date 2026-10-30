package vms

import (
	"context"
	"net/http"
	"net/netip"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

// guestNets is Jail Deck's networks as a guest sees them: platform, whose
// switch is platform, on 10.92.1.0/24. It hands out next, or checks want
// against taken, and remembers whether the seed was written (vm create ran)
// by the time the reservation was let go.
type guestNets struct {
	m       *maker
	taken   map[netip.Addr]bool
	next    netip.Addr
	asked   []netip.Addr
	held    bool
	created bool
}

func (g *guestNets) FindNetwork(_ context.Context, sw string) (OwnNetwork, bool, error) {
	if sw != "platform" {
		return OwnNetwork{}, false, nil
	}
	return OwnNetwork{Name: "platform", Switch: "platform", Subnet: netip.MustParsePrefix("10.92.1.0/24"),
		Gateway: netip.MustParseAddr("10.92.1.1")}, true, nil
}

func (g *guestNets) ReserveAddress(_ context.Context, _ OwnNetwork, want netip.Addr) (netip.Prefix, func(), error) {
	g.asked = append(g.asked, want)
	if want.IsValid() && g.taken[want] {
		return netip.Prefix{}, nil, common.NewAppError(http.StatusConflict, want.String()+" is in use by guest other")
	}
	if !want.IsValid() {
		want = g.next
	}
	g.held = true
	return netip.PrefixFrom(want, 24), func() {
		g.held = false
		g.created = len(g.m.specs) > 0
	}, nil
}

func networkedMaker() (*maker, *Service, *guestNets) {
	m := newMaker()
	m.switches = append(m.switches, "platform")
	s, _ := newMakerService(m)
	nets := &guestNets{m: m, taken: map[netip.Addr]bool{}, next: netip.MustParseAddr("10.92.1.2")}
	s.SetNetworks(nets)
	return m, s, nets
}

func onNetwork(network *StaticNetwork) CreateRequest {
	return CreateRequest{Name: "web", Image: "debian-13", Switch: "platform", Network: network}
}

func TestAGuestOnANetworkIsGivenItsAddress(t *testing.T) {
	m, s, nets := networkedMaker()
	created, err := s.Create(context.Background(), onNetwork(nil))
	if err != nil {
		t.Fatal(err)
	}
	spec := m.specs[0]
	if spec.Address != "10.92.1.2/24" || spec.Gateway != "10.92.1.1" || spec.Nameservers != nil {
		t.Errorf("spec = %+v", spec)
	}
	if created.Network != "platform" || created.Address != "10.92.1.2/24" {
		t.Errorf("created = %+v", created)
	}
	if nets.held || !nets.created {
		t.Errorf("held %v, seed written when let go %v", nets.held, nets.created)
	}
}

func TestAGuestOnANetworkKeepsWhatItAsksFor(t *testing.T) {
	m, s, nets := networkedMaker()
	if _, err := s.Create(context.Background(), onNetwork(&StaticNetwork{Nameservers: []string{"9.9.9.9"}})); err != nil {
		t.Fatal(err)
	}
	if spec := m.specs[0]; spec.Address != "10.92.1.2/24" || spec.Gateway != "10.92.1.1" || spec.Nameservers[0] != "9.9.9.9" {
		t.Errorf("spec = %+v", spec)
	}

	m, s, nets = networkedMaker()
	if _, err := s.Create(context.Background(), onNetwork(&StaticNetwork{Address: "10.92.1.20/24", Gateway: "10.92.1.254"})); err != nil {
		t.Fatal(err)
	}
	if spec := m.specs[0]; spec.Address != "10.92.1.20/24" || spec.Gateway != "10.92.1.254" ||
		nets.asked[0] != netip.MustParseAddr("10.92.1.20") {
		t.Errorf("spec = %+v, asked %v", spec, nets.asked)
	}
}

func TestAGuestOnANetworkIsRefusedBeforeAnythingRuns(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateRequest
		status  int
		message string
	}{
		{"an address in use", onNetwork(&StaticNetwork{Address: "10.92.1.10/24"}), http.StatusConflict, "in use by guest other"},
		{"another prefix", onNetwork(&StaticNetwork{Address: "10.92.1.10/16"}), http.StatusBadRequest, "is a /24"},
		{"no address off Jail Deck's networks", CreateRequest{Name: "web", Image: "debian-13", Switch: "jd-capsw",
			Network: &StaticNetwork{Gateway: "10.77.0.1"}}, http.StatusBadRequest, "is not one of Jail Deck's networks"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, s, nets := networkedMaker()
			nets.taken[netip.MustParseAddr("10.92.1.10")] = true
			_, err := s.Create(context.Background(), test.req)
			wantAppError(t, err, test.status, test.message)
			if len(m.calls) != 0 || nets.held {
				t.Errorf("ran %v, held %v", m.calls, nets.held)
			}
		})
	}
}

func TestAGuestOnAnOperatorsSwitchIsLeftToDHCP(t *testing.T) {
	m, s, nets := networkedMaker()
	if _, err := s.Create(context.Background(), CreateRequest{Name: "web", Image: "debian-13", Switch: "jd-capsw"}); err != nil {
		t.Fatal(err)
	}
	if m.specs[0].Address != "" || len(nets.asked) != 0 {
		t.Errorf("spec = %+v, asked %v", m.specs[0], nets.asked)
	}
}

func TestEnsureOnANetworkComparesTheAddressTheGuestHas(t *testing.T) {
	m, s, nets := networkedMaker()
	if _, err := s.Ensure(context.Background(), "web", onNetwork(nil)); err != nil {
		t.Fatal(err)
	}
	nets.next = netip.MustParseAddr("10.92.1.50")
	again, err := s.Ensure(context.Background(), "web", onNetwork(nil))
	if err != nil || again.Changed || again.Differences != nil || len(nets.asked) != 1 {
		t.Errorf("again = %+v, %v; asked %v", again, err, nets.asked)
	}
	m.applied["web"].Address = "10.77.0.5/24"
	moved, err := s.Ensure(context.Background(), "web", onNetwork(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := Difference{Field: "network.address", Want: "an address of 10.92.1.0/24", Have: "10.77.0.5/24"}
	if len(moved.Differences) != 1 || moved.Differences[0] != want {
		t.Errorf("differences = %+v", moved.Differences)
	}
}

func TestTheDetailSaysWhereToConnect(t *testing.T) {
	_, s, _ := networkedMaker()
	if _, err := s.Create(context.Background(), onNetwork(nil)); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Get(context.Background(), "web")
	if err != nil || detail.Address != "10.92.1.2/24" {
		t.Errorf("detail = %+v, %v", detail, err)
	}
}
