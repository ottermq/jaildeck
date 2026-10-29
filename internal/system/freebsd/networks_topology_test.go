package freebsd

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/ottermq/jaildeck/internal/networks"
)

// The capture: a guest's tap on the private default switch, a jail's epair
// on the operator's switch, and the physical and loopback interfaces around
// them (their addresses replaced).
func TestParseInterfacesReadsTheCapture(t *testing.T) {
	interfaces := parseInterfaces(captured(t, "top_ifconfig"))
	byName := map[string]networks.Interface{}
	names := make([]string, 0)
	for _, i := range interfaces {
		byName[i.Name] = i
		names = append(names, i.Name)
	}
	if want := []string{"em0", "em1", "lo0", "vm-vmlan", "vm-jaildeck", "tap0", "epair0a", "epair0b"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("interfaces = %v\nwant %v", names, want)
	}

	jaildeck := byName["vm-jaildeck"]
	if !reflect.DeepEqual(jaildeck.Members, []networks.BridgeMember{{Name: "tap0", Private: true}}) ||
		!reflect.DeepEqual(jaildeck.Addresses, []netip.Prefix{netip.MustParsePrefix("10.88.0.1/24")}) ||
		!reflect.DeepEqual(jaildeck.Groups, []string{"bridge", "vm-switch", "viid-667b2@"}) {
		t.Errorf("vm-jaildeck = %+v", jaildeck)
	}
	if m := byName["vm-vmlan"].Members; !reflect.DeepEqual(m, []networks.BridgeMember{{Name: "epair0a"}}) {
		t.Errorf("vm-vmlan members = %+v", m)
	}
	if d := byName["tap0"].Description; d != "vmnet/jd-cap-128/0/jaildeck" {
		t.Errorf("tap0 description = %q", d)
	}
	if d := byName["epair0a"].Description; d != "jail:jd-cap-128" {
		t.Errorf("epair0a description = %q", d)
	}
	if a := byName["em0"].Addresses; len(a) != 2 {
		t.Errorf("em0 addresses = %v", a)
	}
	if len(byName["epair0b"].Addresses) != 0 || byName["epair0b"].Description != "" {
		t.Errorf("epair0b = %+v", byName["epair0b"])
	}
}

// vm-bhyve lists a switch whose bridge is gone with no interface.
func TestSwitchWithNoBridgeInTheCapture(t *testing.T) {
	switches := parseSwitchList(captured(t, "top_switch_list"))
	last := switches[len(switches)-1]
	if last.Name != "jdcap128" || last.Bridge != "" || last.Address != "" {
		t.Errorf("last switch = %+v", last)
	}
}

func TestParseInterfacesEdges(t *testing.T) {
	out := "bridge0: flags=8843<UP> metric 0 mtu 1500\n\t\n\tmember: em1 flags=943<PRIVATE>\n" +
		"\tmember: em2 flags=3<LEARNING,DISCOVER>\n\tmember: em3\nnot an interface line\n\tinet 10.0.0.1/24\n"
	interfaces := parseInterfaces(out)
	if len(interfaces) != 1 {
		t.Fatalf("interfaces = %+v", interfaces)
	}
	want := []networks.BridgeMember{{Name: "em1", Private: true}, {Name: "em2"}, {Name: "em3"}}
	if !reflect.DeepEqual(interfaces[0].Members, want) || len(interfaces[0].Addresses) != 0 {
		t.Errorf("bridge0 = %+v", interfaces[0])
	}
}
