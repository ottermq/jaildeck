package jails

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

// stubNetworks is Jail Deck's networks: platform on jdn1, 10.92.1.0/24. It
// hands out next, or checks want against taken, and remembers whether the
// stanza was written by the time the reservation was let go.
type stubNetworks struct {
	taken   map[netip.Addr]bool
	next    netip.Addr
	config  *stubConfig
	asked   []netip.Addr
	held    bool
	written bool
	finds   int
}

var platformNet = OwnNetwork{Name: "platform", Bridge: "jdn1",
	Subnet: netip.MustParsePrefix("10.92.1.0/24"), Gateway: netip.MustParseAddr("10.92.1.1")}

func newStubNetworks(config *stubConfig) *stubNetworks {
	return &stubNetworks{taken: map[netip.Addr]bool{}, next: netip.MustParseAddr("10.92.1.2"), config: config}
}

func (s *stubNetworks) FindNetwork(_ context.Context, name, bridge string) (OwnNetwork, bool, error) {
	s.finds++
	switch {
	case name == "platform", bridge == "jdn1":
		return platformNet, true, nil
	case name != "":
		return OwnNetwork{}, false, common.NewAppError(http.StatusBadRequest, "there is no network named "+name)
	}
	return OwnNetwork{}, false, nil
}

func (s *stubNetworks) ReserveAddress(_ context.Context, n OwnNetwork, want netip.Addr) (netip.Prefix, func(), error) {
	s.asked = append(s.asked, want)
	if want.IsValid() && s.taken[want] {
		return netip.Prefix{}, nil, common.NewAppError(http.StatusConflict, want.String()+" is in use by jail other")
	}
	if !want.IsValid() {
		want = s.next
	}
	s.held = true
	return netip.PrefixFrom(want, 24), func() {
		s.held = false
		_, s.written = s.config.written["db1"]
	}, nil
}

func networkedService() (*JailService, *stubNetworks, *stubConfig, *stubBridges) {
	system, cloner, config, autostart, logger, bridges, epairs, cleaner := vnetParts()
	bridges.have = append(bridges.have, "jdn1")
	service := vnetService(system, cloner, config, autostart, logger, bridges, epairs, cleaner)
	nets := newStubNetworks(config)
	service.SetNetworks(nets)
	return service, nets, config, bridges
}

func onPlatform(ip4, gateway string) Spec {
	spec := vnetSpec()
	spec.Network = Network{Mode: NetworkVNET, Name: "platform", IP4: ip4, Gateway: gateway}
	return spec
}

func TestAJailOnANetworkIsGivenItsAddress(t *testing.T) {
	service, nets, config, _ := networkedService()
	result, err := service.Create(context.Background(), onPlatform("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if result.Network != "platform" || result.Address != "10.92.1.2/24" {
		t.Errorf("network, address = %q, %q", result.Network, result.Address)
	}
	stanza := config.written["db1"]
	for _, want := range []string{
		`exec.prestart += "ifconfig jdn1 addm epair7a";`,
		`exec.start += "ifconfig epair7b inet 10.92.1.2/24 up";`,
		`exec.start += "route -q add default 10.92.1.1";`,
	} {
		if !strings.Contains(stanza, want) {
			t.Errorf("the stanza lacks %s:\n%s", want, stanza)
		}
	}
	if nets.held || !nets.written {
		t.Errorf("held %v, stanza written when let go %v: the network must be held until the stanza says the address",
			nets.held, nets.written)
	}
}

func TestAJailOnANetworksBridgeIsGivenItsAddressToo(t *testing.T) {
	service, nets, config, _ := networkedService()
	spec := vnetSpec()
	spec.Network = Network{Mode: NetworkVNET, Bridge: "jdn1", Private: true}
	result, err := service.Create(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Address != "10.92.1.2/24" || !strings.Contains(config.written["db1"], "inet 10.92.1.2/24 up") ||
		len(nets.asked) != 1 {
		t.Errorf("result = %+v, asked %v", result, nets.asked)
	}
}

func TestAJailAsksForItsOwnAddressAndGateway(t *testing.T) {
	service, nets, config, _ := networkedService()
	result, err := service.Create(context.Background(), onPlatform("10.92.1.10", "10.92.1.254"))
	if err != nil {
		t.Fatal(err)
	}
	if nets.asked[0] != netip.MustParseAddr("10.92.1.10") || result.Address != "10.92.1.10/24" {
		t.Errorf("asked %v, result %+v", nets.asked, result)
	}
	if !strings.Contains(config.written["db1"], "route -q add default 10.92.1.254") {
		t.Errorf("the gateway asked for is not the stanza's:\n%s", config.written["db1"])
	}
}

func TestAJailOnANetworkIsRefusedBeforeAnythingExists(t *testing.T) {
	tests := []struct {
		name    string
		spec    Spec
		status  int
		message string
	}{
		{"an address in use", onPlatform("10.92.1.10/24", ""), http.StatusConflict, "in use by jail other"},
		{"another prefix", onPlatform("10.92.1.10/16", ""), http.StatusBadRequest, "is a /24"},
		{"no such network", onPlatform("", ""), http.StatusBadRequest, "no network named acme"},
	}
	tests[2].spec.Network.Name = "acme"
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, nets, config, _ := networkedService()
			nets.taken[netip.MustParseAddr("10.92.1.10")] = true
			result, err := service.Create(context.Background(), test.spec)
			wantCode(t, err, test.status, test.message)
			if result.FailedAt != StepCheck || len(config.written) != 0 || nets.held {
				t.Errorf("result = %+v, written %v, held %v", result, config.written, nets.held)
			}
		})
	}
}

func TestAJailOnAnOperatorsBridgeIsLeftAsItsSpecSays(t *testing.T) {
	service, nets, config, _ := networkedService()
	result, err := service.Create(context.Background(), vnetSpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(nets.asked) != 0 || result.Network != "" || result.Address != "" ||
		!strings.Contains(config.written["db1"], "inet 10.77.0.23/24 up") {
		t.Errorf("asked %v, result %+v", nets.asked, result)
	}
}

func TestANetworkByNameNeedsTheNetworks(t *testing.T) {
	system, cloner, config, autostart, logger, bridges, epairs, cleaner := vnetParts()
	service := vnetService(system, cloner, config, autostart, logger, bridges, epairs, cleaner)
	_, err := service.Create(context.Background(), onPlatform("", ""))
	wantCode(t, err, http.StatusNotImplemented, "give network.bridge")
}

func TestEnsureOnANetworkComparesTheAddressTheJailHas(t *testing.T) {
	host := newEnsureHost()
	host.bridges.have = append(host.bridges.have, "jdn1")
	service := host.service()
	nets := newStubNetworks(host.config)
	service.SetNetworks(nets)

	if _, err := service.Ensure(context.Background(), "db1", onPlatform("", "")); err != nil {
		t.Fatal(err)
	}
	// The same spec again, with another address free: what the jail has
	// is still the answer, and nothing is reserved.
	nets.next = netip.MustParseAddr("10.92.1.50")
	second, err := service.Ensure(context.Background(), "db1", onPlatform("", ""))
	if err != nil || len(second.Differences) != 0 || len(nets.asked) != 1 {
		t.Errorf("second = %+v, %v; asked %v", second.Differences, err, nets.asked)
	}
	// Named by its bridge, it is the same jail.
	spec := onPlatform("", "")
	spec.Network.Name, spec.Network.Bridge = "", "jdn1"
	if third, err := service.Ensure(context.Background(), "db1", spec); err != nil || len(third.Differences) != 0 {
		t.Errorf("by bridge = %+v, %v", third.Differences, err)
	}
	// An address the spec asks for is compared as it is.
	if fourth, _ := service.Ensure(context.Background(), "db1", onPlatform("10.92.1.9/24", "")); len(fourth.Differences) == 0 {
		t.Error("another address asked for is no difference")
	}

	// A jail whose address is not the network's is told apart.
	host.edit("db1", func(s string) string { return strings.Replace(s, "10.92.1.2/24", "10.77.0.5/24", 1) })
	fifth, err := service.Ensure(context.Background(), "db1", onPlatform("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(fifth.Differences) == 0 || fifth.Differences[0] != (Difference{Field: "network.ip4",
		Want: "an address of 10.92.1.0/24", Have: "10.77.0.5/24"}) {
		t.Errorf("differences = %+v", fifth.Differences)
	}
}

func TestANetworkSpecIsChecked(t *testing.T) {
	tests := map[string]Network{
		"network.bridge and network.name are both set": {Mode: NetworkVNET, Bridge: "jdn1", Name: "platform"},
		`network.name is only read for mode "vnet"`:    {Mode: NetworkDisable, Name: "platform"},
		"which is not a network name":                  {Mode: NetworkVNET, Name: "Platform!"},
		// Each of these is written into the stanza as it is.
		`network.ip4 is "10.0.0.5/24 up\"; exec.prestart += \"touch /x"`: {Mode: NetworkVNET, Bridge: "bridge0",
			IP4: `10.0.0.5/24 up"; exec.prestart += "touch /x`},
		"network.gateway is": {Mode: NetworkVNET, Bridge: "bridge0", IP4: "10.0.0.5/24", Gateway: `10.0.0.1"; x`},
		"network.gateway is \"2001:db8::1\", which is not an IPv4 address": {Mode: NetworkVNET, Bridge: "bridge0",
			IP4: "10.0.0.5/24", Gateway: "2001:db8::1"},
		"network.interface is":              {Mode: NetworkAlias, IP4: "10.0.0.5", Interface: `em0"; x`},
		"network.bridge is":                 {Mode: NetworkVNET, Bridge: "bridge0 addm em0"},
		"network.ip4 is \"2001:db8::5/64\"": {Mode: NetworkAlias, IP4: "2001:db8::5/64", Interface: "em0"},
	}
	for message, network := range tests {
		spec := validSpec()
		spec.Network = network
		_, err := spec.normalize()
		wantCode(t, err, http.StatusBadRequest, message)
	}
	for _, ok := range []Network{
		{Mode: NetworkAlias, IP4: "192.0.2.10", Interface: "em0"},
		{Mode: NetworkAlias, IP4: "192.0.2.10/24", Interface: "vtnet0.10"},
		{Mode: NetworkVNET, Name: "default"},
	} {
		spec := validSpec()
		spec.Network = ok
		if _, err := spec.normalize(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
}

func wantCode(t *testing.T, err error, code int, message string) {
	t.Helper()
	appErr, ok := err.(*common.AppError)
	if !ok {
		t.Fatalf("err = %v, want an AppError %d", err, code)
	}
	if appErr.Code != code || !strings.Contains(appErr.Message, message) {
		t.Errorf("err = %d %q, want %d containing %q", appErr.Code, appErr.Message, code, message)
	}
}
