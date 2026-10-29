package fake

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"github.com/ottermq/jaildeck/internal/networks"
)

// Networks is an in-memory host for the default VM network: vm-bhyve with
// the one switch the fake's running guest is on, forwarding off, and PF not
// loaded -- so a fresh setup has Jail Deck's parts to put in place and the
// operator's to be told about.
type Networks struct {
	mu         sync.Mutex
	switches   []networks.Switch
	forwarding string
	gateway    string
	files      map[string]string
	pfEnabled  bool
	pfRules    string
	anchors    map[string][]string
	// bridges are the tenant networks' jdn<i> bridges (JD-043), and guests
	// says which guests are on a switch: the fake VM host's, when joined.
	bridges map[string]*networks.TenantBridge
	guests  func(sw string) []string
}

func NewNetworks() *Networks {
	return &Networks{
		switches:   []networks.Switch{{Name: "jd-capsw", Type: "standard", Bridge: "vm-jd-capsw", Address: "10.77.0.1/24"}},
		forwarding: "0",
		gateway:    "NO",
		files:      map[string]string{},
		anchors:    map[string][]string{},
		bridges:    map[string]*networks.TenantBridge{},
	}
}

// GuestsFrom joins the fake VM host, which knows which guest is on which
// switch.
func (f *Networks) GuestsFrom(guests func(sw string) []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.guests = guests
}

// EnablePF stands in for the operator enabling PF with pf.conf including
// Jail Deck's anchors, for tests and fake mode.
func (f *Networks) EnablePF(rules string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pfEnabled, f.pfRules = true, rules
}

func (f *Networks) Switches(context.Context) ([]networks.Switch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]networks.Switch(nil), f.switches...), nil
}

func (f *Networks) Sysctl(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forwarding, nil
}

func (f *Networks) RCConf(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name == "pf_rules" {
		return "/etc/pf.conf", nil
	}
	return f.gateway, nil
}

func (f *Networks) DefaultRouteInterface(context.Context) (string, error) { return "em0", nil }

func (f *Networks) PF(context.Context) (networks.PF, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return networks.PF{Loaded: f.pfEnabled, Enabled: f.pfEnabled,
		NatAnchor: strings.Contains(f.pfRules, `nat-anchor "jaildeck/*"`), RulesFile: "/etc/pf.conf", RulesText: f.pfRules}, nil
}

func (f *Networks) AnchorRules(_ context.Context, anchor string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.anchors[anchor]...), nil
}

func (f *Networks) AnchorFile(_ context.Context, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.files[path], nil
}

func (f *Networks) CreateSwitch(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switches = append(f.switches, networks.Switch{Name: name, Type: "standard", Bridge: "vm-" + name})
	return "vm switch create " + name, nil
}

func (f *Networks) SetSwitchAddress(_ context.Context, name, address string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.switches {
		if f.switches[i].Name == name {
			f.switches[i].Address = address
		}
	}
	return "vm switch address " + name + " " + address, nil
}

func (f *Networks) SetSwitchPrivate(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.switches {
		if f.switches[i].Name == name {
			f.switches[i].Private = true
		}
	}
	return "vm switch private " + name + " on", nil
}

func (f *Networks) SetSysctl(_ context.Context, name, value string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forwarding = value
	return "sysctl " + name + "=" + value, nil
}

func (f *Networks) SetRCConf(_ context.Context, name, value string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gateway = value
	return "sysrc " + name + "=" + value, nil
}

func (f *Networks) WriteAnchorFile(_ context.Context, path, content string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = content
	return "write " + path, nil
}

// LoadAnchor loads the file's nat lines, as pfctl prints them back.
func (f *Networks) LoadAnchor(_ context.Context, anchor, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rules := []string{}
	for _, line := range strings.Split(f.files[path], "\n") {
		if strings.HasPrefix(line, "nat ") {
			rules = append(rules, line+" round-robin")
		}
	}
	f.anchors[anchor] = rules
	return "pfctl -n -a " + anchor + " -f " + path + " && pfctl -a " + anchor + " -f " + path, nil
}

func (f *Networks) TenantBridges(context.Context) ([]networks.TenantBridge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := make([]networks.TenantBridge, 0, len(f.bridges))
	for _, b := range f.bridges {
		copied := *b
		copied.Members = append([]string{}, b.Members...)
		list = append(list, copied)
	}
	return list, nil
}

// AddressesInUse is the fake host's: its LAN, and the switches' addresses.
func (f *Networks) AddressesInUse(context.Context) ([]netip.Prefix, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	for _, sw := range f.switches {
		if p, err := netip.ParsePrefix(sw.Address); err == nil {
			in = append(in, p.Masked())
		}
	}
	for _, b := range f.bridges {
		if b.Address.IsValid() {
			in = append(in, b.Address.Masked())
		}
	}
	return in, nil
}

func (f *Networks) CreateTenantBridge(_ context.Context, bridge, description string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bridges[bridge] = &networks.TenantBridge{Name: bridge, Description: description, Members: []string{}}
	return "ifconfig bridge create name " + bridge + " description " + description + " up", nil
}

func (f *Networks) SetBridgeAddress(_ context.Context, bridge, address string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.bridges[bridge]; ok {
		b.Address, _ = netip.ParsePrefix(address)
	}
	return "ifconfig " + bridge + " inet " + address, nil
}

func (f *Networks) DestroyBridge(_ context.Context, bridge string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.bridges, bridge)
	return "ifconfig " + bridge + " destroy", nil
}

func (f *Networks) CreateManualSwitch(_ context.Context, name, bridge string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switches = append(f.switches, networks.Switch{Name: name, Type: "manual", Bridge: bridge})
	return "vm switch create -t manual -b " + bridge + " " + name, nil
}

func (f *Networks) DestroySwitch(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switches = slices.DeleteFunc(f.switches, func(s networks.Switch) bool { return s.Name == name })
	return "vm switch destroy " + name + " && sysrc -i -f /zroot/vm/.config/system.conf -x bridge_" + name, nil
}

func (f *Networks) SwitchUsers(_ context.Context, name string) ([]string, error) {
	f.mu.Lock()
	guests := f.guests
	f.mu.Unlock()
	if guests == nil {
		return []string{}, nil
	}
	return guests(name), nil
}

// BridgeUsers: the fake's jails have no VNET stanzas naming a tenant bridge.
func (f *Networks) BridgeUsers(context.Context, string) ([]string, error) { return []string{}, nil }

var _ networks.System = (*Networks)(nil)
