package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/audit"
)

type fakeLogger struct{ entries []audit.Entry }

func (f *fakeLogger) Log(_ context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return nil
}

var _ audit.Logger = (*fakeLogger)(nil)

// stubSystem answers with the facts a test sets, and runs nothing. Test
// doubles live here rather than in internal/system/fake, which imports this
// package.
type stubSystem struct {
	sysctl      map[string]string
	sysctlErr   map[string]error
	defaults    map[string]string
	defaultsErr error
	bridges     []string
	bridgesErr  error
	rulesets    []DevfsRuleset
	rulesetsErr error
	cpu         CPUVirtualization
	cpuErr      error
	pf          PFStatus
	pfErr       error
	pools       []string
	poolsErr    error
	datasets    map[string][]Dataset
	datasetsErr error
	createErr   map[string]error

	cpuAsked bool
	// creates records every CreateDataset call, in order.
	creates []createCall
}

type createCall struct {
	name  string
	props map[string]string
}

func (s *stubSystem) Sysctl(_ context.Context, name string) (string, error) {
	if err := s.sysctlErr[name]; err != nil {
		return "", err
	}
	return s.sysctl[name], nil
}
func (s *stubSystem) JailConfDefaults(context.Context) (map[string]string, error) {
	return s.defaults, s.defaultsErr
}
func (s *stubSystem) Bridges(context.Context) ([]string, error) { return s.bridges, s.bridgesErr }
func (s *stubSystem) DevfsRulesets(context.Context) ([]DevfsRuleset, error) {
	return s.rulesets, s.rulesetsErr
}
func (s *stubSystem) CPUVirtualization(context.Context) (CPUVirtualization, error) {
	s.cpuAsked = true
	return s.cpu, s.cpuErr
}
func (s *stubSystem) PFStatus(context.Context) (PFStatus, error) { return s.pf, s.pfErr }
func (s *stubSystem) Pools(context.Context) ([]string, error)    { return s.pools, s.poolsErr }
func (s *stubSystem) Datasets(_ context.Context, pool string) ([]Dataset, error) {
	return s.datasets[pool], s.datasetsErr
}

// CreateDataset records the call and, unless told to fail, adds the dataset
// with the mountpoint zfs would give it.
func (s *stubSystem) CreateDataset(_ context.Context, name string, props map[string]string) (DatasetResult, error) {
	s.creates = append(s.creates, createCall{name, props})
	result := DatasetResult{Dataset: name, Command: "zfs create " + name}
	if err := s.createErr[name]; err != nil {
		return result, err
	}
	pool, _, _ := strings.Cut(name, "/")
	mountpoint := props["mountpoint"]
	if mountpoint == "" {
		mountpoint = jailsMountpoint + strings.TrimPrefix(name, pool+"/jails")
	}
	s.datasets[pool] = append(s.datasets[pool], Dataset{Name: name, Mountpoint: mountpoint})
	return result, nil
}

var _ HostSystem = (*stubSystem)(nil)

// readyHost passes every check.
func readyHost() *stubSystem {
	return &stubSystem{
		sysctl:   map[string]string{racctOID: "1", archOID: "amd64"},
		defaults: map[string]string{"exec.timeout": "120"},
		bridges:  []string{"bridge0"},
		rulesets: []DevfsRuleset{{Name: "devfsrules_jail", Number: 4}},
		cpu:      CPUVirtualization{Reported: true, Features: []string{"PAT", "EPT", "UG", "VPID"}},
		pf:       PFStatus{Loaded: true, Enabled: true},
		pools:    []string{"tank"},
		datasets: map[string][]Dataset{"tank": fullLayout("tank")},
	}
}

func fullLayout(pool string) []Dataset {
	return []Dataset{
		{Name: pool, Mountpoint: "none"},
		{Name: pool + "/jails", Mountpoint: "/usr/local/jails"},
		{Name: pool + "/jails/containers", Mountpoint: "/usr/local/jails/containers"},
		{Name: pool + "/jails/containers/web", Mountpoint: "/usr/local/jails/containers/web"},
		{Name: pool + "/jails/media", Mountpoint: "/usr/local/jails/media"},
		{Name: pool + "/jails/templates", Mountpoint: "/usr/local/jails/templates"},
	}
}

func newTestService(system HostSystem) *Service {
	return NewService(system, &fakeLogger{}, "")
}

func checkByID(t *testing.T, r Report, id CheckID) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("report has no %s check", id)
	return Check{}
}

func TestDeriveCapability(t *testing.T) {
	tests := []struct {
		gates []CheckStatus
		want  CapabilityStatus
	}{
		{[]CheckStatus{CheckPass}, CapabilityReady},
		{[]CheckStatus{CheckPass, CheckPass}, CapabilityReady},
		{[]CheckStatus{CheckFail}, CapabilityUnavailable},
		{[]CheckStatus{CheckUnknown}, CapabilityUnknown},
		{[]CheckStatus{CheckPass, CheckUnknown}, CapabilityUnknown},
		// A failure outranks an unknown, whichever comes first.
		{[]CheckStatus{CheckUnknown, CheckFail}, CapabilityUnavailable},
		{[]CheckStatus{CheckFail, CheckUnknown, CheckPass}, CapabilityUnavailable},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.gates), func(t *testing.T) {
			if got := deriveCapability(tt.gates); got != tt.want {
				t.Errorf("deriveCapability(%v) = %s, want %s", tt.gates, got, tt.want)
			}
		})
	}
}

func TestReadinessChecks(t *testing.T) {
	cmdErr := errors.New("exit status 1: devfs rule: ioctl DEVFSIO_SGETNEXT: Operation not permitted")
	tests := []struct {
		name         string
		modify       func(*stubSystem)
		id           CheckID
		want         CheckStatus
		wantObserved string
		wantRemedy   string // a substring the remedy must contain
		wantDetail   string // likewise for the detail
	}{
		{name: "racct 1", id: CheckRacct, want: CheckPass, wantObserved: "1"},
		{name: "racct 0", id: CheckRacct, want: CheckFail, wantObserved: "0",
			modify:     func(s *stubSystem) { s.sysctl[racctOID] = "0" },
			wantRemedy: "sysrc -f /boot/loader.conf kern.racct.enable=1", wantDetail: "present, but disabled"},
		// No RACCT in the kernel: the loader.conf remedy would be wrong here.
		{name: "racct oid missing", id: CheckRacct, want: CheckUnknown,
			modify:     func(s *stubSystem) { s.sysctlErr = map[string]error{racctOID: ErrUnknownOID} },
			wantDetail: "support not present in kernel"},
		{name: "racct unreadable", id: CheckRacct, want: CheckUnknown,
			modify: func(s *stubSystem) { s.sysctlErr = map[string]error{racctOID: context.DeadlineExceeded} }},
		{name: "racct unexpected value", id: CheckRacct, want: CheckUnknown, wantObserved: "2",
			modify: func(s *stubSystem) { s.sysctl[racctOID] = "2" }},

		{name: "exec.timeout set", id: CheckExecTimeout, want: CheckPass, wantObserved: "120"},
		{name: "jail.conf without exec.timeout", id: CheckExecTimeout, want: CheckFail,
			modify:     func(s *stubSystem) { s.defaults = map[string]string{"exec.clean": ""} },
			wantRemedy: "echo 'exec.timeout = 120;' >> /etc/jail.conf", wantDetail: "JD-014"},
		// isengard: jails run from jail.conf.d with no jail.conf at all.
		{name: "no jail.conf", id: CheckExecTimeout, want: CheckFail,
			modify:     func(s *stubSystem) { s.defaults = nil },
			wantRemedy: `.include "/etc/jail.conf.d/*.conf";\n' >> /etc/jail.conf`, wantDetail: "does not exist"},
		{name: "jail.conf unreadable", id: CheckExecTimeout, want: CheckUnknown,
			modify: func(s *stubSystem) { s.defaultsErr = errors.New("permission denied") }},

		{name: "bridges", id: CheckBridges, want: CheckPass, wantObserved: "bridge0, bridge1",
			modify: func(s *stubSystem) { s.bridges = []string{"bridge0", "bridge1"} }},
		{name: "no bridges", id: CheckBridges, want: CheckFail,
			modify: func(s *stubSystem) { s.bridges = []string{} }, wantRemedy: "ifconfig bridge0 create"},
		{name: "bridges unreadable", id: CheckBridges, want: CheckUnknown,
			modify: func(s *stubSystem) { s.bridgesErr = errors.New("exit status 1") }},

		{name: "devfs rulesets", id: CheckDevfsRulesets, want: CheckPass, wantObserved: "devfsrules_jail=4, 9",
			modify: func(s *stubSystem) { s.rulesets = []DevfsRuleset{{Name: "devfsrules_jail", Number: 4}, {Number: 9}} }},
		{name: "no devfs rulesets", id: CheckDevfsRulesets, want: CheckFail,
			modify: func(s *stubSystem) { s.rulesets = []DevfsRuleset{} }, wantRemedy: "service devfs restart"},
		// Unprivileged showsets: the kernel would not say, which is not "none".
		{name: "devfs unprivileged", id: CheckDevfsRulesets, want: CheckUnknown,
			modify: func(s *stubSystem) { s.rulesetsErr = cmdErr }, wantDetail: "Operation not permitted"},

		{name: "VT-x with EPT and UG", id: CheckCPUVirtualization, want: CheckPass, wantObserved: "PAT,EPT,UG,VPID"},
		{name: "VT-x without UG", id: CheckCPUVirtualization, want: CheckFail, wantObserved: "PAT,EPT",
			modify:     func(s *stubSystem) { s.cpu.Features = []string{"PAT", "EPT"} },
			wantRemedy: "EPT and UG", wantDetail: "reports no UG"},
		{name: "VT-x without EPT or UG", id: CheckCPUVirtualization, want: CheckFail,
			modify:     func(s *stubSystem) { s.cpu.Features = []string{"PAT"} },
			wantDetail: "reports no EPT or UG"},
		// Not captured on any host: constructed from the fact alone.
		{name: "VT-x disabled in BIOS", id: CheckCPUVirtualization, want: CheckFail,
			modify:     func(s *stubSystem) { s.cpu.DisabledInBIOS = true },
			wantRemedy: "firmware setup"},
		{name: "no VT-x line", id: CheckCPUVirtualization, want: CheckUnknown,
			modify: func(s *stubSystem) { s.cpu = CPUVirtualization{} }, wantDetail: "not evidence"},
		{name: "boot log unreadable", id: CheckCPUVirtualization, want: CheckUnknown,
			modify: func(s *stubSystem) { s.cpuErr = errors.New("permission denied") }},
		{name: "armv7", id: CheckCPUVirtualization, want: CheckUnknown, wantObserved: "armv7",
			modify: func(s *stubSystem) { s.sysctl[archOID] = "armv7" }, wantDetail: "x86 hosts only"},
		{name: "i386 is x86", id: CheckCPUVirtualization, want: CheckPass,
			modify: func(s *stubSystem) { s.sysctl[archOID] = "i386" }},
		{name: "arch unreadable, VT-x line decides", id: CheckCPUVirtualization, want: CheckPass,
			modify: func(s *stubSystem) { s.sysctlErr = map[string]error{archOID: errors.New("exit status 1")} }},

		{name: "PF enabled", id: CheckPF, want: CheckPass, wantObserved: "Enabled"},
		{name: "PF disabled", id: CheckPF, want: CheckFail, wantObserved: "Disabled",
			modify: func(s *stubSystem) { s.pf = PFStatus{Loaded: true} }, wantRemedy: "service pf start"},
		{name: "PF not loaded", id: CheckPF, want: CheckFail, wantObserved: "not loaded",
			modify: func(s *stubSystem) { s.pf = PFStatus{} }, wantRemedy: "service pf start", wantDetail: "/etc/pf.conf"},
		{name: "PF unreadable", id: CheckPF, want: CheckUnknown,
			modify: func(s *stubSystem) { s.pfErr = errors.New("exit status 1: pfctl: /dev/pf: Permission denied") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := readyHost()
			if tt.modify != nil {
				tt.modify(system)
			}
			report, err := newTestService(system).Readiness(context.Background())
			if err != nil {
				t.Fatalf("Readiness() error = %v", err)
			}
			c := checkByID(t, report, tt.id)
			if c.Status != tt.want {
				t.Errorf("status = %s, want %s (detail %q)", c.Status, tt.want, c.Detail)
			}
			if tt.wantObserved != "" && c.Observed != tt.wantObserved {
				t.Errorf("observed = %q, want %q", c.Observed, tt.wantObserved)
			}
			// A remedy on every failure, and only there: unknown carries none,
			// because there may be nothing to fix.
			if (c.Remedy != "") != (c.Status == CheckFail) {
				t.Errorf("status %s with remedy %q", c.Status, c.Remedy)
			}
			if !strings.Contains(c.Remedy, tt.wantRemedy) {
				t.Errorf("remedy = %q, want it to contain %q", c.Remedy, tt.wantRemedy)
			}
			if !strings.Contains(c.Detail, tt.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", c.Detail, tt.wantDetail)
			}
		})
	}
}

func TestReadinessSkipsVTxOffX86(t *testing.T) {
	system := readyHost()
	system.sysctl[archOID] = "armv7"
	if _, err := newTestService(system).Readiness(context.Background()); err != nil {
		t.Fatal(err)
	}
	if system.cpuAsked {
		t.Error("asked for the VT-x line on armv7, where there is none to find")
	}
}

func TestReadinessShape(t *testing.T) {
	system := readyHost()
	system.sysctl[racctOID] = "0"
	system.cpu = CPUVirtualization{}
	report, err := newTestService(system).Readiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var gotChecks []CheckID
	for _, c := range report.Checks {
		gotChecks = append(gotChecks, c.ID)
	}
	wantChecks := []CheckID{CheckRacct, CheckExecTimeout, CheckBridges, CheckDevfsRulesets,
		CheckZFSPool, CheckZFSLayout, CheckCPUVirtualization, CheckPF}
	if !slices.Equal(gotChecks, wantChecks) {
		t.Errorf("checks = %v, want %v", gotChecks, wantChecks)
	}

	want := map[CapabilityID]struct {
		status  CapabilityStatus
		gatedBy []CheckID
	}{
		CapabilityJails:        {CapabilityReady, []CheckID{CheckExecTimeout}},
		CapabilityLimits:       {CapabilityUnavailable, []CheckID{CheckRacct}},
		CapabilityVNET:         {CapabilityReady, []CheckID{CheckBridges}},
		CapabilityProvisioning: {CapabilityReady, []CheckID{CheckDevfsRulesets, CheckZFSPool, CheckZFSLayout}},
		CapabilityVMs:          {CapabilityUnknown, []CheckID{CheckCPUVirtualization}},
		CapabilityFirewall:     {CapabilityReady, []CheckID{CheckPF}},
	}
	var gotCaps []CapabilityID
	for _, c := range report.Capabilities {
		gotCaps = append(gotCaps, c.ID)
		w := want[c.ID]
		if c.Status != w.status || !slices.Equal(c.GatedBy, w.gatedBy) {
			t.Errorf("%s = %s gated by %v, want %s gated by %v", c.ID, c.Status, c.GatedBy, w.status, w.gatedBy)
		}
	}
	wantCaps := []CapabilityID{CapabilityJails, CapabilityLimits, CapabilityVNET, CapabilityProvisioning, CapabilityVMs, CapabilityFirewall}
	if !slices.Equal(gotCaps, wantCaps) {
		t.Errorf("capabilities = %v, want %v", gotCaps, wantCaps)
	}
}

// Every slice in the response marshals to [], never null: clients iterate
// them without a guard.
func TestReadinessJSONHasNoNulls(t *testing.T) {
	report, err := newTestService(readyHost()).Readiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		t.Errorf("response contains null: %s", b)
	}
	if !strings.Contains(string(b), `"remedy":""`) {
		t.Errorf("remedy should be present even when empty: %s", b)
	}
}

func TestReadinessReturnsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newTestService(readyHost()).Readiness(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Readiness() error = %v, want context.Canceled", err)
	}
}
