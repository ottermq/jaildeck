package networks

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
)

// host is a scripted host for the default network: what it has, what each
// write does to it, and which write fails.
type host struct {
	switches   []Switch
	forwarding string
	gateway    string
	egress     string
	pf         PF
	anchor     []string
	files      map[string]string
	fail       map[string]error
	ran        []string
	// Tenant networks (JD-043): the jdn bridges, other addresses on the
	// host, and who uses what.
	bridges     map[string]*TenantBridge
	addresses   []netip.Prefix
	switchUsers map[string][]string
	bridgeUsers map[string][]string
	// interfaces are what ifconfig would list, for the Networks view (#128).
	interfaces []Interface
	// uses are what AddressUses finds, and usesCalls how often it was asked.
	uses      []Use
	usesCalls int
}

func freshHost() *host {
	return &host{forwarding: "0", gateway: "NO", egress: "em0", files: map[string]string{}, fail: map[string]error{},
		pf: PF{RulesFile: "/etc/pf.conf"}, bridges: map[string]*TenantBridge{},
		switchUsers: map[string][]string{}, bridgeUsers: map[string][]string{}}
}

func (h *host) do(call string) error {
	h.ran = append(h.ran, call)
	return h.fail[strings.Fields(call)[0]]
}

func (h *host) Switches(context.Context) ([]Switch, error) { return slices.Clone(h.switches), nil }
func (h *host) Sysctl(context.Context, string) (string, error) {
	return h.forwarding, nil
}
func (h *host) RCConf(context.Context, string) (string, error)         { return h.gateway, nil }
func (h *host) DefaultRouteInterface(context.Context) (string, error)  { return h.egress, nil }
func (h *host) PF(context.Context) (PF, error)                         { return h.pf, nil }
func (h *host) AnchorRules(context.Context, string) ([]string, error)  { return h.anchor, nil }
func (h *host) AnchorFile(_ context.Context, p string) (string, error) { return h.files[p], nil }

func (h *host) CreateSwitch(_ context.Context, name string) (string, error) {
	if err := h.do("create " + name); err != nil {
		return "vm switch create " + name, err
	}
	h.switches = append(h.switches, Switch{Name: name, Type: "standard", Bridge: "vm-" + name})
	return "vm switch create " + name, nil
}

func (h *host) TenantBridges(context.Context) ([]TenantBridge, error) {
	if err := h.fail["bridges"]; err != nil {
		return nil, err
	}
	list := make([]TenantBridge, 0, len(h.bridges))
	for _, b := range h.bridges {
		list = append(list, *b)
	}
	slices.SortFunc(list, func(a, b TenantBridge) int { return strings.Compare(a.Name, b.Name) })
	return list, nil
}

func (h *host) AddressesInUse(context.Context) ([]netip.Prefix, error) {
	in := slices.Clone(h.addresses)
	for _, b := range h.bridges {
		if b.Address.IsValid() {
			in = append(in, b.Address.Masked())
		}
	}
	return in, nil
}

func (h *host) CreateTenantBridge(_ context.Context, bridge, description string) (string, error) {
	err := h.do("bridge " + bridge)
	// Not atomic, as measured: the bridge is there even when it failed.
	h.bridges[bridge] = &TenantBridge{Name: bridge, Description: description}
	return "ifconfig bridge create name " + bridge, err
}

func (h *host) SetBridgeAddress(_ context.Context, bridge, address string) (string, error) {
	if err := h.do("inet " + address); err != nil {
		return "ifconfig " + bridge + " inet " + address, err
	}
	h.bridges[bridge].Address = netip.MustParsePrefix(address)
	return "ifconfig " + bridge + " inet " + address, nil
}

func (h *host) DestroyBridge(_ context.Context, bridge string) (string, error) {
	if err := h.do("unbridge " + bridge); err != nil {
		return "ifconfig " + bridge + " destroy", err
	}
	delete(h.bridges, bridge)
	return "ifconfig " + bridge + " destroy", nil
}

func (h *host) CreateManualSwitch(_ context.Context, name, bridge string) (string, error) {
	if err := h.do("manual " + name); err != nil {
		return "vm switch create -t manual", err
	}
	h.switches = append(h.switches, Switch{Name: name, Type: "manual", Bridge: bridge})
	return "vm switch create -t manual -b " + bridge + " " + name, nil
}

func (h *host) DestroySwitch(_ context.Context, name string) (string, error) {
	if err := h.do("unswitch " + name); err != nil {
		return "vm switch destroy " + name, err
	}
	h.switches = slices.DeleteFunc(h.switches, func(s Switch) bool { return s.Name == name })
	return "vm switch destroy " + name, nil
}

func (h *host) SwitchUsers(_ context.Context, name string) ([]string, error) {
	return h.switchUsers[name], nil
}

func (h *host) BridgeUsers(_ context.Context, bridge string) ([]string, error) {
	return h.bridgeUsers[bridge], nil
}

func (h *host) AddressUses(context.Context, netip.Prefix, string) ([]Use, error) {
	h.usesCalls++
	if err := h.fail["uses"]; err != nil {
		return nil, err
	}
	return h.uses, nil
}

func (h *host) Interfaces(context.Context) ([]Interface, error) {
	if err := h.fail["interfaces"]; err != nil {
		return nil, err
	}
	return h.interfaces, nil
}

func (h *host) sw(name string) *Switch {
	for i := range h.switches {
		if h.switches[i].Name == name {
			return &h.switches[i]
		}
	}
	return nil
}

func (h *host) SetSwitchAddress(_ context.Context, name, address string) (string, error) {
	if err := h.do("address " + address); err != nil {
		return "", err
	}
	h.sw(name).Address = address
	return "vm switch address " + name + " " + address, nil
}

func (h *host) SetSwitchPrivate(_ context.Context, name string) (string, error) {
	if err := h.do("private " + name); err != nil {
		return "", err
	}
	h.sw(name).Private = true
	return "vm switch private " + name + " on", nil
}

func (h *host) SetSysctl(_ context.Context, name, value string) (string, error) {
	if err := h.do("sysctl " + name); err != nil {
		return "sysctl " + name + "=" + value, err
	}
	h.forwarding = value
	return "sysctl " + name + "=" + value, nil
}

func (h *host) SetRCConf(_ context.Context, name, value string) (string, error) {
	if err := h.do("sysrc " + name); err != nil {
		return "", err
	}
	h.gateway = value
	return "sysrc " + name + "=" + value, nil
}

func (h *host) WriteAnchorFile(_ context.Context, path, content string) (string, error) {
	if err := h.do("write " + path); err != nil {
		return "write " + path, err
	}
	h.files[path] = content
	return "write " + path, nil
}

func (h *host) LoadAnchor(_ context.Context, anchor, path string) (string, error) {
	if err := h.do("load " + anchor); err != nil {
		return "", err
	}
	h.anchor = nil
	for _, line := range strings.Split(h.files[path], "\n") {
		if strings.HasPrefix(line, "nat ") {
			h.anchor = append(h.anchor, line+" round-robin")
		}
	}
	return "pfctl -n -a " + anchor + " -f " + path + " && pfctl -a " + anchor + " -f " + path, nil
}

type recorder struct{ entries []audit.Entry }

func (r *recorder) Log(_ context.Context, e audit.Entry) error {
	r.entries = append(r.entries, e)
	return nil
}

func (r *recorder) ops() []string {
	out := []string{}
	for _, e := range r.entries {
		ok := "ok"
		if !e.Success {
			ok = "failed"
		}
		out = append(out, e.Operation+" "+ok)
	}
	return out
}

var settings = Settings{Switch: "jaildeck", Address: "10.88.0.1/24", AnchorDir: "/usr/local/etc/jaildeck/pf",
	Range: "10.92.0.0/16"}

func service(h *host) (*Service, *recorder) {
	rec := &recorder{}
	return NewService(h, rec, settings), rec
}

func part(t *testing.T, d Default, id string) Part {
	t.Helper()
	for _, p := range d.Parts {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no part %s in %+v", id, d.Parts)
	return Part{}
}

func wantStatus(t *testing.T, d Default, want map[string]PartStatus) {
	t.Helper()
	for id, status := range want {
		if got := part(t, d, id).Status; got != status {
			t.Errorf("%s = %s, want %s (%+v)", id, got, status, part(t, d, id))
		}
	}
}

func wantAppError(t *testing.T, err error, code int, contains string) {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != code || !strings.Contains(appErr.Message, contains) {
		t.Errorf("err = %v, want %d containing %q", err, code, contains)
	}
}

// pfReady is the operator's half done: PF enabled, and pf.conf including
// Jail Deck's anchors, loaded.
func pfReady(h *host) {
	s := NewService(h, nil, settings)
	h.pf = PF{Loaded: true, Enabled: true, NatAnchor: true,
		RulesFile: "/etc/pf.conf", RulesText: "set skip on lo0\n" + strings.Join(s.PFLines(), "\n") + "\npass all\n"}
}

// --- reading ---------------------------------------------------------------

func TestAFreshHostSaysWhatIsMissingAndWhose(t *testing.T) {
	s, _ := service(freshHost())
	d, err := s.Default(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.Switch != "jaildeck" || d.Subnet != "10.88.0.0/24" || d.Gateway != "10.88.0.1" || d.Egress != "em0" {
		t.Errorf("default = %+v", d)
	}
	if d.Usable || d.Ready {
		t.Errorf("usable %v, ready %v, want neither", d.Usable, d.Ready)
	}
	wantStatus(t, d, map[string]PartStatus{
		PartSwitch: PartFail, PartAddress: PartUnknown, PartPrivate: PartUnknown,
		PartForwarding: PartFail, PartForwardingAtBoot: PartFail, PartAnchorFile: PartFail,
		PartPF: PartFail, PartPFRules: PartFail, PartAnchorLoaded: PartUnknown,
	})
	if p := part(t, d, PartPF); p.Owner != OwnerOperator || !strings.Contains(p.Remedy, "pf_enable=YES") ||
		!strings.Contains(p.Detail, "drop that session") {
		t.Errorf("pf part = %+v", p)
	}
	rules := part(t, d, PartPFRules)
	for _, line := range []string{`nat-anchor "jaildeck/*"`, `anchor "jaildeck/*"`,
		`load anchor "jaildeck/vmnet" from "/usr/local/etc/jaildeck/pf/vmnet.conf"`, "service pf reload"} {
		if !strings.Contains(rules.Remedy, line) {
			t.Errorf("pf_rules remedy lacks %q:\n%s", line, rules.Remedy)
		}
	}
}

func TestPFConfEditedAndNotReloadedSaysReload(t *testing.T) {
	h := freshHost()
	pfReady(h)
	h.pf.NatAnchor = false
	s, _ := service(h)
	d, _ := s.Default(context.Background())
	if p := part(t, d, PartPFRules); p.Status != PartFail || p.Remedy != "service pf reload" {
		t.Errorf("pf_rules = %+v", p)
	}
}

func TestNoDefaultRouteIsTheOperatorsToSettle(t *testing.T) {
	h := freshHost()
	h.egress = ""
	s, _ := service(h)
	d, _ := s.Default(context.Background())
	if p := part(t, d, PartAnchorFile); p.Owner != OwnerOperator || !strings.Contains(p.Remedy, "JAILDECK_VM_EGRESS") {
		t.Errorf("anchor_file = %+v", p)
	}
	if _, err := s.EnsureDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(h.ran, func(c string) bool { return strings.HasPrefix(c, "write") }) {
		t.Errorf("wrote a NAT rule with no interface to send it out of: %v", h.ran)
	}
}

func TestBadSettingsAreRefused(t *testing.T) {
	for _, address := range []string{"10.88.0.0/24", "10.88.0.1", "fd00::1/64", "10.88.0.1/31", "nonsense"} {
		s := NewService(freshHost(), nil, Settings{Switch: "jaildeck", Address: address, AnchorDir: "/x"})
		_, err := s.Default(context.Background())
		wantAppError(t, err, http.StatusInternalServerError, "JAILDECK_VM_ADDRESS")
	}
}

func TestNatLoadedReadsPfctlsOwnForm(t *testing.T) {
	subnet, egress := mustPrefix("10.88.0.0/24"), "em0"
	measured := []string{"nat on em0 inet from 10.88.0.0/24 to any -> (em0) round-robin"}
	if !natLoaded(measured, subnet, egress) {
		t.Error("the rule pfctl printed back, measured, was not recognised")
	}
	for _, other := range [][]string{
		{"nat on em0 inet from 10.89.0.0/24 to any -> (em0) round-robin"},
		{"nat on em1 inet from 10.88.0.0/24 to any -> (em1) round-robin"},
		nil,
	} {
		if natLoaded(other, subnet, egress) {
			t.Errorf("%v was taken for the network's rule", other)
		}
	}
}

// --- putting it in place -----------------------------------------------------

func TestEnsureOnAFreshHostPutsJailDecksPartsInPlace(t *testing.T) {
	h := freshHost()
	s, rec := service(h)
	ensured, err := s.EnsureDefault(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantRan := []string{"create jaildeck", "address 10.88.0.1/24", "private jaildeck",
		"sysctl net.inet.ip.forwarding", "sysrc gateway_enable", "write /usr/local/etc/jaildeck/pf/vmnet.conf"}
	if !slices.Equal(h.ran, wantRan) {
		t.Errorf("ran %v\nwant %v", h.ran, wantRan)
	}
	wantOps := []string{"create-vm-switch ok", "set-vm-switch-address ok", "set-vm-switch-private ok",
		"enable-ip-forwarding ok", "enable-ip-forwarding-at-boot ok", "write-pf-anchor ok"}
	if !slices.Equal(rec.ops(), wantOps) {
		t.Errorf("audit %v\nwant %v", rec.ops(), wantOps)
	}
	if !ensured.Changed || !ensured.Usable || ensured.Ready {
		t.Errorf("changed %v usable %v ready %v; want changed, usable, not ready (PF is the operator's)",
			ensured.Changed, ensured.Usable, ensured.Ready)
	}
	if !strings.Contains(ensured.Note, "next start") {
		t.Errorf("note = %q, want the private ports' timing said", ensured.Note)
	}
	if want := "nat on em0 inet from 10.88.0.0/24 to any -> (em0)\n"; !strings.HasSuffix(h.files["/usr/local/etc/jaildeck/pf/vmnet.conf"], want) {
		t.Errorf("anchor file = %q", h.files["/usr/local/etc/jaildeck/pf/vmnet.conf"])
	}
	if part(t, ensured.Default, PartPF).Status != PartFail {
		t.Error("PF was reported as done by Jail Deck")
	}
}

func TestEnsureTwiceDoesNothingTheSecondTime(t *testing.T) {
	h := freshHost()
	s, rec := service(h)
	if _, err := s.EnsureDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.ran, rec.entries = nil, nil
	ensured, err := s.EnsureDefault(context.Background())
	if err != nil || ensured.Changed || len(h.ran) != 0 || len(rec.entries) != 0 {
		t.Errorf("second ensure: %+v, %v, ran %v, audited %v", ensured, err, h.ran, rec.ops())
	}
}

func TestEnsureLoadsTheAnchorOncePFIsUp(t *testing.T) {
	h := freshHost()
	pfReady(h)
	s, rec := service(h)
	ensured, err := s.EnsureDefault(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ensured.Ready {
		t.Errorf("not ready after the ensure with PF up: %+v", ensured.Parts)
	}
	if got := rec.ops(); got[len(got)-1] != "load-pf-anchor ok" {
		t.Errorf("audit = %v, want the anchor loaded last", got)
	}
}

func TestANewUplinkRewritesAndReloadsTheRules(t *testing.T) {
	h := freshHost()
	pfReady(h)
	s, _ := service(h)
	if _, err := s.EnsureDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.egress, h.ran = "igb0", nil
	if _, err := s.EnsureDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.ran, []string{"write /usr/local/etc/jaildeck/pf/vmnet.conf", "load jaildeck/vmnet"}) {
		t.Errorf("ran %v, want the file rewritten and reloaded", h.ran)
	}
	if len(h.anchor) != 1 || !strings.Contains(h.anchor[0], "-> (igb0)") {
		t.Errorf("anchor = %v", h.anchor)
	}
}

func TestAHalfMadeSwitchIsAdopted(t *testing.T) {
	h := freshHost()
	h.switches = []Switch{{Name: "jaildeck", Type: "standard", Bridge: "vm-jaildeck"}}
	s, _ := service(h)
	if _, err := s.EnsureDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(h.ran, "create jaildeck") || !slices.Contains(h.ran, "address 10.88.0.1/24") ||
		!slices.Contains(h.ran, "private jaildeck") {
		t.Errorf("ran %v, want the switch kept and finished", h.ran)
	}
}

func TestEnsureRefusesRatherThanClobbers(t *testing.T) {
	for name, sw := range map[string]Switch{
		"another address": {Name: "jaildeck", Type: "standard", Bridge: "vm-jaildeck", Address: "10.99.0.1/24"},
		"a manual switch": {Name: "jaildeck", Type: "manual", Bridge: "bridge0"},
	} {
		t.Run(name, func(t *testing.T) {
			h := freshHost()
			h.switches = []Switch{sw}
			s, rec := service(h)
			_, err := s.EnsureDefault(context.Background())
			wantAppError(t, err, http.StatusConflict, "")
			if len(h.ran) != 0 || len(rec.entries) != 0 {
				t.Errorf("ran %v, audited %v", h.ran, rec.ops())
			}
		})
	}
}

func TestAStepThatFailsStopsTheRestAndSaysWhatWasDone(t *testing.T) {
	h := freshHost()
	h.fail["sysctl"] = errors.New("sysctl: net.inet.ip.forwarding: Operation not permitted")
	s, rec := service(h)
	_, err := s.EnsureDefault(context.Background())
	wantAppError(t, err, http.StatusInternalServerError, "after switch, address, private")
	if slices.ContainsFunc(h.ran, func(c string) bool { return strings.HasPrefix(c, "sysrc") }) {
		t.Errorf("carried on after a failure: %v", h.ran)
	}
	if got := rec.ops(); got[len(got)-1] != "enable-ip-forwarding failed" {
		t.Errorf("audit = %v, want the failure recorded", got)
	}
}

func mustPrefix(s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		panic(err)
	}
	return p
}

// Someone edited the file and loaded it: the rule the network needs is there,
// so the loaded anchor passes, but so are their extra rules. Rewriting the
// file must reload it too, or the extras stay in force until the next boot.
func TestARewrittenFileIsReloadedEvenWhenTheRuleWasLoaded(t *testing.T) {
	h := freshHost()
	pfReady(h)
	s, _ := service(h)
	if _, err := s.EnsureDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := "/usr/local/etc/jaildeck/pf/vmnet.conf"
	h.files[path] += "pass in quick all\n"
	h.anchor = append(h.anchor, "pass in quick all")
	h.ran = nil
	if _, err := s.EnsureDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.ran, []string{"write " + path, "load jaildeck/vmnet"}) {
		t.Errorf("ran %v, want the file rewritten and reloaded", h.ran)
	}
}

func TestPFConfWithSomeOfTheLinesStillFails(t *testing.T) {
	h := freshHost()
	pfReady(h)
	h.pf.RulesText = `nat-anchor "jaildeck/*"` + "\npass all\n"
	s, _ := service(h)
	d, _ := s.Default(context.Background())
	if p := part(t, d, PartPFRules); p.Status != PartFail || !strings.Contains(p.Detail, "lacks 2 of the three lines") {
		t.Errorf("pf_rules = %+v", p)
	}
}

// A rule out of another interface that happens to translate to egress's
// address is not the network's rule.
func TestNatLoadedChecksTheInterfaceItGoesOutOf(t *testing.T) {
	if natLoaded([]string{"nat on em1 inet from 10.88.0.0/24 to any -> (em0) round-robin"}, mustPrefix("10.88.0.0/24"), "em0") {
		t.Error("a rule on em1 was taken for one on em0")
	}
}

func TestHasLineIsNotASubstringSearch(t *testing.T) {
	text := "  nat-anchor \"jaildeck/*\" all\n# anchor \"jaildeck/*\" -- a comment, not a rule\nanchor \"other/*\"\n"
	if hasLine(text, `anchor "jaildeck/*"`) {
		t.Error(`found anchor "jaildeck/*" inside nat-anchor, or in a comment`)
	}
	if !hasLine(text, `nat-anchor "jaildeck/*"`) {
		t.Error("missed nat-anchor with an option after it")
	}
}
