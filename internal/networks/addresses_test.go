package networks

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

// A host with the default network set up and one tenant network, platform,
// on jdn1: what isengard has, as JD-047's measurements read it.
func addressedHost() *host {
	h := readyHost()
	h.bridges["jdn1"] = &TenantBridge{Name: "jdn1", Description: "jaildeck:network=platform;tenant=otter",
		Address: netip.MustParsePrefix("10.92.1.1/24")}
	return h
}

func platform(t *testing.T, s *Service) Own {
	t.Helper()
	n, ok, err := s.Find(context.Background(), Ref{Name: "platform"})
	if err != nil || !ok {
		t.Fatalf("Find(platform) = %+v, %v, %v", n, ok, err)
	}
	return n
}

func TestFindingANetworkByWhatARequestNames(t *testing.T) {
	s, _ := service(addressedHost())
	ctx := context.Background()
	want := Own{Name: "platform", Bridge: "jdn1", Switch: "platform",
		Subnet: netip.MustParsePrefix("10.92.1.0/24"), Gateway: netip.MustParseAddr("10.92.1.1")}
	for _, ref := range []Ref{{Name: "platform"}, {Bridge: "jdn1"}, {Switch: "platform"}} {
		if n, ok, err := s.Find(ctx, ref); err != nil || !ok || n != want {
			t.Errorf("Find(%+v) = %+v, %v, %v", ref, n, ok, err)
		}
	}
	def := Own{Name: "default", Bridge: "vm-jaildeck", Switch: "jaildeck",
		Subnet: netip.MustParsePrefix("10.88.0.0/24"), Gateway: netip.MustParseAddr("10.88.0.1")}
	for _, ref := range []Ref{{Name: "default"}, {Switch: "jaildeck"}, {Bridge: "vm-jaildeck"}} {
		if n, ok, err := s.Find(ctx, ref); err != nil || !ok || n != def {
			t.Errorf("Find(%+v) = %+v, %v, %v", ref, n, ok, err)
		}
	}
	// An operator's bridge or switch is not Jail Deck's to address.
	for _, ref := range []Ref{{Bridge: "bridge0"}, {Switch: "vmlan"}} {
		if _, ok, err := s.Find(ctx, ref); err != nil || ok {
			t.Errorf("Find(%+v) = %v, %v; want not one of Jail Deck's", ref, ok, err)
		}
	}
	// A name can only mean one of them.
	_, _, err := s.Find(ctx, Ref{Name: "acme"})
	wantAppError(t, err, http.StatusBadRequest, `there is no network named "acme"; it has "default" and "platform"`)
}

func TestTheDefaultNetworkIsOnlyOneWhenItsSwitchHasItsAddress(t *testing.T) {
	h := addressedHost()
	h.switches[0].Address = ""
	s, _ := service(h)
	if _, ok, err := s.Find(context.Background(), Ref{Switch: "jaildeck"}); err != nil || ok {
		t.Errorf("a default switch without its address is not a network to address: %v, %v", ok, err)
	}
	_, _, err := s.Find(context.Background(), Ref{Name: "default"})
	wantAppError(t, err, http.StatusBadRequest, `it has "platform"`)
}

func TestUsesAreOnePerAddressAndNameTheirOwner(t *testing.T) {
	h := addressedHost()
	h.uses = []Use{
		{Address: "10.92.1.20", Kind: UseSeen},
		{Address: "10.92.1.20", Kind: UseJail, Name: "otter-api"},
		{Address: "10.92.1.10", Kind: UseJail, Name: "otter-db"},
		{Address: "10.92.1.10", Kind: UseSeen},
		{Address: "10.92.1.1", Kind: UseJail, Name: "otter-db"}, // its route: the gateway
		{Address: "10.92.1.33", Kind: UseSeen},
		{Address: "10.88.0.5", Kind: UseGuest, Name: "elsewhere"},
		{Address: "10.92.1.9", Kind: UseGuest, Name: "web"},
	}
	s, _ := service(h)
	uses, err := s.Uses(context.Background(), platform(t, s))
	if err != nil {
		t.Fatal(err)
	}
	want := []Use{
		{Address: "10.92.1.9", Kind: UseGuest, Name: "web"},
		{Address: "10.92.1.10", Kind: UseJail, Name: "otter-db"},
		{Address: "10.92.1.20", Kind: UseJail, Name: "otter-api"},
		{Address: "10.92.1.33", Kind: UseSeen},
	}
	if !reflect.DeepEqual(uses, want) {
		t.Errorf("uses = %+v\nwant   %+v", uses, want)
	}
}

func TestReservingPicksTheLowestFreeAddress(t *testing.T) {
	h := addressedHost()
	h.uses = []Use{{Address: "10.92.1.2", Kind: UseJail, Name: "a"}, {Address: "10.92.1.3", Kind: UseSeen}}
	s, _ := service(h)
	lease, release, err := s.Reserve(context.Background(), platform(t, s), netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // twice is harmless
	if lease.Address != netip.MustParsePrefix("10.92.1.4/24") || lease.Gateway != netip.MustParseAddr("10.92.1.1") {
		t.Errorf("lease = %+v", lease)
	}

	// The default network's gateway need not be its first address.
	h = addressedHost()
	h.switches[0].Address = "10.88.0.2/24"
	s = NewService(h, &recorder{}, Settings{Switch: "jaildeck", Address: "10.88.0.2/24", Range: "10.92.0.0/16"})
	def, _, _ := s.Find(context.Background(), Ref{Name: "default"})
	lease, release, err = s.Reserve(context.Background(), def, netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	release()
	if lease.Address != netip.MustParsePrefix("10.88.0.1/24") {
		t.Errorf("lease = %+v, want .1, the gateway being .2", lease)
	}
}

func TestReservingAnAddressAsked(t *testing.T) {
	h := addressedHost()
	h.uses = []Use{{Address: "10.92.1.10", Kind: UseJail, Name: "otter-db"}, {Address: "10.92.1.11", Kind: UseSeen}}
	s, _ := service(h)
	n := platform(t, s)
	ctx := context.Background()

	lease, release, err := s.Reserve(ctx, n, netip.MustParseAddr("10.92.1.20"))
	if err != nil || lease.Address != netip.MustParsePrefix("10.92.1.20/24") {
		t.Fatalf("Reserve(.20) = %+v, %v", lease, err)
	}
	release()

	_, _, err = s.Reserve(ctx, n, netip.MustParseAddr("10.92.1.10"))
	wantAppError(t, err, http.StatusConflict, `10.92.1.10 on the network "platform" is in use by jail otter-db`)
	_, _, err = s.Reserve(ctx, n, netip.MustParseAddr("10.92.1.11"))
	wantAppError(t, err, http.StatusConflict, "the bridge has seen")
	for addr, why := range map[string]string{
		"10.92.2.5":   "outside 10.92.1.0/24",
		"10.92.1.0":   "the subnet's own address",
		"10.92.1.255": "broadcast",
		"10.92.1.1":   "the gateway",
	} {
		calls := h.usesCalls
		_, _, err := s.Reserve(ctx, n, netip.MustParseAddr(addr))
		wantAppError(t, err, http.StatusBadRequest, why)
		if h.usesCalls != calls {
			t.Errorf("%s: refused only after reading the host", addr)
		}
	}
	// A refusal lets the network go: the next one is not held up.
	if _, release, err := s.Reserve(ctx, n, netip.Addr{}); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}

func TestReservingRefusesWhatCannotBeRead(t *testing.T) {
	h := addressedHost()
	h.fail["uses"] = errors.New("arp: interface jdn1 does not exist")
	s, _ := service(h)
	n := platform(t, s)
	_, _, err := s.Reserve(context.Background(), n, netip.Addr{})
	wantAppError(t, err, http.StatusInternalServerError, "jdn1")
	delete(h.fail, "uses")
	if _, release, err := s.Reserve(context.Background(), n, netip.Addr{}); err != nil {
		t.Fatalf("a failed read held the network: %v", err)
	} else {
		release()
	}
}

func TestAFullNetworkIsRefused(t *testing.T) {
	h := addressedHost()
	for a := netip.MustParseAddr("10.92.1.2"); a.Less(netip.MustParseAddr("10.92.1.255")); a = a.Next() {
		h.uses = append(h.uses, Use{Address: a.String(), Kind: UseSeen})
	}
	s, _ := service(h)
	_, _, err := s.Reserve(context.Background(), platform(t, s), netip.Addr{})
	wantAppError(t, err, http.StatusConflict, "no free address left in 10.92.1.0/24")
	h.uses = h.uses[:len(h.uses)-1]
	lease, release, err := s.Reserve(context.Background(), platform(t, s), netip.Addr{})
	if err != nil || lease.Address.Addr() != netip.MustParseAddr("10.92.1.254") {
		t.Fatalf("the last address: %+v, %v", lease, err)
	}
	release()
}

// The lock is what keeps two creates from one address: the second waits
// until the first has written its address down, then reads it.
func TestASecondReservationWaitsForTheFirst(t *testing.T) {
	h := addressedHost()
	s, _ := service(h)
	n := platform(t, s)
	first, release, err := s.Reserve(context.Background(), n, netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan Lease)
	go func() {
		lease, release, err := s.Reserve(context.Background(), n, netip.Addr{})
		if err != nil {
			t.Error(err)
		}
		release()
		got <- lease
	}()
	select {
	case <-got:
		t.Fatal("the second reservation ran while the first held the network")
	case <-time.After(50 * time.Millisecond):
	}
	// The first create writes its address, where the second one reads it.
	h.uses = append(h.uses, Use{Address: first.Address.Addr().String(), Kind: UseJail, Name: "first"})
	release()
	if second := <-got; second.Address == first.Address {
		t.Errorf("both were given %s", first.Address)
	}

	// Another network is not held up by this one.
	def, _, _ := s.Find(context.Background(), Ref{Name: "default"})
	_, hold, err := s.Reserve(context.Background(), n, netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	done := make(chan struct{})
	go func() {
		_, release, err := s.Reserve(context.Background(), def, netip.Addr{})
		if err == nil {
			release()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a reservation on the default network waited for platform's")
	}
}

func TestTheViewsListTheAddressesInUse(t *testing.T) {
	h := addressedHost()
	h.uses = []Use{{Address: "10.92.1.10", Kind: UseJail, Name: "otter-db"}}
	s, _ := service(h)
	n, err := s.Network(context.Background(), "platform")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(n.Addresses, h.uses) {
		t.Errorf("addresses = %+v", n.Addresses)
	}
	list, err := s.Networks(context.Background())
	if err != nil || !reflect.DeepEqual(list.Networks[0].Addresses, h.uses) {
		t.Errorf("list = %+v, %v", list, err)
	}
	// Unreadable, the view still shows the network, without them.
	h.fail["uses"] = errors.New("arp failed")
	if n, err := s.Network(context.Background(), "platform"); err != nil || n.Addresses != nil {
		t.Errorf("network = %+v, %v", n, err)
	}
}
