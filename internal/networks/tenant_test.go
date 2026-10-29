package networks

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// The rules are the ones capture-tenant-network-induced.sh measured on
// isengard: each tenant's pass to its own gateway before its block of every
// other VM network and of the host ("self"), and NAT out (JD-043).

func readyHost() *host {
	h := freshHost()
	h.switches = []Switch{{Name: "jaildeck", Type: "standard", Bridge: "vm-jaildeck", Address: "10.88.0.1/24", Private: true}}
	h.addresses = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("10.88.0.0/24")}
	return h
}

const anchorPath = "/usr/local/etc/jaildeck/pf/vmnet.conf"

// oks is operations as the recorder lists successful ones.
func oks(operations ...string) []string {
	out := make([]string, len(operations))
	for i, op := range operations {
		out[i] = op + " ok"
	}
	return out
}

func TestCreatingANetworkMakesItsPartsInOrder(t *testing.T) {
	h := readyHost()
	h.pf = PF{Loaded: true, Enabled: true, RulesFile: "/etc/pf.conf"}
	s, rec := service(h)
	n, err := s.CreateNetwork(context.Background(), CreateNetworkRequest{Name: "acme-prod", Tenant: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	want := Network{Name: "acme-prod", Tenant: "acme", Bridge: "jdn0", Subnet: "10.92.0.0/24", Gateway: "10.92.0.1", Members: []string{}}
	if n.Name != want.Name || n.Tenant != want.Tenant || n.Bridge != want.Bridge || n.Subnet != want.Subnet || n.Gateway != want.Gateway {
		t.Errorf("network = %+v\nwant      %+v", n, want)
	}
	if !slices.Equal(rec.ops(), oks(OperationCreateNetworkBridge, OperationSetNetworkAddress, OperationCreateSwitch,
		OperationWriteAnchor, OperationLoadAnchor)) {
		t.Errorf("audited %v", rec.ops())
	}
	if b := h.bridges["jdn0"]; b == nil || b.Description != "jaildeck:network=acme-prod;tenant=acme" ||
		b.Address != netip.MustParsePrefix("10.92.0.1/24") {
		t.Errorf("bridge = %+v", b)
	}
	if sw := h.sw("acme-prod"); sw == nil || sw.Type != "manual" || sw.Bridge != "jdn0" {
		t.Errorf("switch = %+v", sw)
	}
	file := h.files[anchorPath]
	for _, line := range []string{
		"nat on em0 inet from 10.88.0.0/24 to any -> (em0)",
		"nat on em0 inet from 10.92.0.0/24 to any -> (em0)",
		"pass in quick on jdn0 inet from 10.92.0.0/24 to 10.92.0.1 no state",
		"block in quick on jdn0 inet from 10.92.0.0/24 to { 10.88.0.0/24, self } no state",
		"block in quick on vm-jaildeck inet from 10.88.0.0/24 to { 10.92.0.0/24 } no state",
	} {
		if !hasLine(file, line) {
			t.Errorf("the anchor file lacks %q:\n%s", line, file)
		}
	}
	// The pass before the block: the block of self first took the gateway too.
	if strings.Index(file, "pass in quick on jdn0") > strings.Index(file, "block in quick on jdn0") {
		t.Errorf("the block comes before the pass:\n%s", file)
	}
}

// Each network blocks every other, the default's included, and nothing loads
// while PF is down.
func TestTwoNetworksBlockEachOther(t *testing.T) {
	h := readyHost()
	s, rec := service(h)
	for _, name := range []string{"acme", "globex"} {
		if _, err := s.CreateNetwork(context.Background(), CreateNetworkRequest{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	file := h.files[anchorPath]
	for _, line := range []string{
		"block in quick on jdn0 inet from 10.92.0.0/24 to { 10.88.0.0/24, 10.92.1.0/24, self } no state",
		"block in quick on jdn1 inet from 10.92.1.0/24 to { 10.88.0.0/24, 10.92.0.0/24, self } no state",
		"block in quick on vm-jaildeck inet from 10.88.0.0/24 to { 10.92.0.0/24, 10.92.1.0/24 } no state",
	} {
		if !hasLine(file, line) {
			t.Errorf("the anchor file lacks %q:\n%s", line, file)
		}
	}
	if slices.Contains(rec.ops(), OperationLoadAnchor+" ok") {
		t.Error("loaded an anchor with PF down")
	}
	// Translation before filtering, or pfctl refuses the file: measured, a
	// second network's NAT after the first one's rules failed to load.
	lastNat, firstFilter := -1, len(file)
	for i, line := range strings.Split(file, "\n") {
		switch {
		case strings.HasPrefix(line, "nat "):
			lastNat = i
		case strings.HasPrefix(line, "pass ") || strings.HasPrefix(line, "block "):
			firstFilter = min(firstFilter, i)
		}
	}
	if lastNat > firstFilter {
		t.Errorf("a nat rule comes after a filter rule:\n%s", file)
	}
}

// A /24 the host already has an address in is skipped, and so is one a jdn
// bridge holds.
func TestAllocationSkipsWhatIsTaken(t *testing.T) {
	h := readyHost()
	h.addresses = append(h.addresses, netip.MustParsePrefix("10.92.0.0/24"))
	h.bridges["jdn1"] = &TenantBridge{Name: "jdn1"}
	s, _ := service(h)
	n, err := s.CreateNetwork(context.Background(), CreateNetworkRequest{Name: "acme"})
	if err != nil || n.Bridge != "jdn2" || n.Subnet != "10.92.2.0/24" {
		t.Errorf("network = %+v, %v; want jdn2 on 10.92.2.0/24", n, err)
	}
}

func TestCreatingANetworkRefuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		req    CreateNetworkRequest
		host   func(*host, *Settings)
		code   int
		reason string
	}{
		{"an upper-case name", CreateNetworkRequest{Name: "Acme"}, nil, http.StatusBadRequest, "not a network name"},
		{"the default network's name", CreateNetworkRequest{Name: "default"}, nil, http.StatusBadRequest, `"default"`},
		{"a tenant with a space", CreateNetworkRequest{Name: "acme", Tenant: "acme corp"}, nil, http.StatusBadRequest, "tenant is"},
		{"a switch's name", CreateNetworkRequest{Name: "jaildeck"}, nil, http.StatusConflict, "already has a switch"},
		{"a full range", CreateNetworkRequest{Name: "acme"}, func(h *host, s *Settings) {
			s.Range = "10.92.7.0/24"
			h.addresses = append(h.addresses, netip.MustParsePrefix("10.92.7.0/24"))
		}, http.StatusConflict, "no free /24"},
		{"a range that is not one", CreateNetworkRequest{Name: "acme"}, func(_ *host, s *Settings) { s.Range = "10.92.0.0/25" },
			http.StatusInternalServerError, "JAILDECK_NETWORK_RANGE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := readyHost()
			st := settings
			if tc.host != nil {
				tc.host(h, &st)
			}
			rec := &recorder{}
			s := NewService(h, rec, st)
			_, err := s.CreateNetwork(context.Background(), tc.req)
			wantAppError(t, err, tc.code, tc.reason)
			if len(h.ran) != 0 || len(rec.entries) != 0 {
				t.Errorf("ran %v, audited %v", h.ran, rec.ops())
			}
		})
	}
}

// A step that fails takes back what the earlier ones made, and the anchor
// file is as it was: a network without its rules is one others can reach.
func TestAFailedCreateLeavesNothing(t *testing.T) {
	for _, step := range []string{"bridge", "inet", "manual", "load"} {
		t.Run(step, func(t *testing.T) {
			h := readyHost()
			h.pf = PF{Loaded: true, Enabled: true, RulesFile: "/etc/pf.conf"}
			s, _ := service(h)
			h.files[anchorPath] = "before"
			h.fail[step] = errors.New(step + " refused")
			_, err := s.CreateNetwork(context.Background(), CreateNetworkRequest{Name: "acme"})
			if err == nil {
				t.Fatal("the create succeeded")
			}
			if len(h.bridges) != 0 || h.sw("acme") != nil {
				t.Errorf("left bridges %v, switch %v", h.bridges, h.sw("acme"))
			}
			if step == "load" && strings.Contains(h.files[anchorPath], "10.92.0.0/24") {
				t.Errorf("the anchor file still has the network:\n%s", h.files[anchorPath])
			}
		})
	}
}

func TestNetworksAreReadOffTheBridges(t *testing.T) {
	h := readyHost()
	h.bridges["jdn3"] = &TenantBridge{Name: "jdn3", Description: "jaildeck:network=acme-prod;tenant=acme",
		Address: netip.MustParsePrefix("10.92.3.1/24"), Members: []string{"tap4", "epair2a"}}
	h.bridges["jdn9"] = &TenantBridge{Name: "jdn9", Description: "someone else's"}
	s, _ := service(h)
	list, err := s.Networks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list.Range != "10.92.0.0/16" || len(list.Networks) != 1 {
		t.Fatalf("list = %+v", list)
	}
	n := list.Networks[0]
	if n.Name != "acme-prod" || n.Tenant != "acme" || n.Subnet != "10.92.3.0/24" || n.Gateway != "10.92.3.1" ||
		!slices.Equal(n.Members, []string{"tap4", "epair2a"}) {
		t.Errorf("network = %+v", n)
	}
	_, err = s.Network(context.Background(), "nosuch")
	wantAppError(t, err, http.StatusNotFound, "no network named")
}

func TestDestroyingANetwork(t *testing.T) {
	h := readyHost()
	s, rec := service(h)
	ctx := context.Background()
	for _, name := range []string{"acme", "globex"} {
		if _, err := s.CreateNetwork(ctx, CreateNetworkRequest{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.DestroyNetwork(ctx, "acme", "")
	wantAppError(t, err, http.StatusBadRequest, "confirm=acme")
	_, err = s.DestroyNetwork(ctx, "nosuch", "nosuch")
	wantAppError(t, err, http.StatusNotFound, "no network named")

	audited := len(rec.entries)
	if _, err := s.DestroyNetwork(ctx, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rec.ops()[audited:], oks(OperationDestroySwitch, OperationDestroyNetworkBridge, OperationWriteAnchor)) {
		t.Errorf("audited %v", rec.ops()[audited:])
	}
	if h.bridges["jdn0"] != nil || h.sw("acme") != nil {
		t.Error("the bridge or the switch is still there")
	}
	file := h.files[anchorPath]
	if strings.Contains(file, "10.92.0.0/24") || !hasLine(file,
		"block in quick on jdn1 inet from 10.92.1.0/24 to { 10.88.0.0/24, self } no state") {
		t.Errorf("the anchor file after:\n%s", file)
	}
}

func TestANetworkInUseIsNotDestroyed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		use   func(*host)
		names string
	}{
		{"a guest names its switch", func(h *host) { h.switchUsers["acme"] = []string{"web"} }, "guest web"},
		{"a jail names its bridge", func(h *host) { h.bridgeUsers["jdn0"] = []string{"db"} }, "jail db"},
		{"an interface is on it", func(h *host) { h.bridges["jdn0"].Members = []string{"tap7"} }, "interfaces tap7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := readyHost()
			s, rec := service(h)
			if _, err := s.CreateNetwork(context.Background(), CreateNetworkRequest{Name: "acme"}); err != nil {
				t.Fatal(err)
			}
			tc.use(h)
			audited := len(rec.entries)
			_, err := s.DestroyNetwork(context.Background(), "acme", "acme")
			wantAppError(t, err, http.StatusConflict, tc.names)
			if len(rec.entries) != audited || h.bridges["jdn0"] == nil {
				t.Errorf("a refused destroy ran %v", rec.ops()[audited:])
			}
		})
	}
}

// The default network's file is exactly what it was with no tenant network,
// so a host that set the default network up before JD-043 does not see it as
// changed; and with tenants, the default network's anchor part reads the
// whole file.
func TestTheDefaultNetworkSeesTheWholeFile(t *testing.T) {
	plain := anchorContent(netip.MustParsePrefix("10.88.0.0/24"), "vm-jaildeck", "em0", nil)
	if plain != "# Written by Jail Deck: NAT for the default VM network (JD-040).\n"+
		"# Rewritten when it differs; edits here do not last.\n"+
		"nat on em0 inet from 10.88.0.0/24 to any -> (em0)\n" {
		t.Errorf("the default network's own file changed:\n%s", plain)
	}

	h := readyHost()
	s, _ := service(h)
	if _, err := s.CreateNetwork(context.Background(), CreateNetworkRequest{Name: "acme"}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Default(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p := part(t, d, PartAnchorFile); p.Status != PartPass {
		t.Errorf("anchor part = %+v; the file written with the tenant network is the one expected", p)
	}
}
