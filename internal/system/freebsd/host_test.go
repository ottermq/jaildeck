package freebsd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/system"
)

// Every test here parses bytes a real FreeBSD host printed, from
// testdata/hostfacts (see its README). Where a state has no capture, the test
// says it is constructed rather than presenting it as host output.

const hostfacts = "testdata/hostfacts"

// fixture is the path of one captured file: set is the host directory, name
// the capture, and ext one of stdout, stderr or exit.
func fixture(set, name, ext string) string {
	return filepath.Join(hostfacts, set, name+"."+ext)
}

// replayRunner answers each command the adapter runs with a capture, keyed by
// the command line. The captures ran some commands under doas; Jail Deck runs
// as root (JD-001), so the key is the command without it.
type replayRunner struct {
	t        *testing.T
	set      string
	captures map[string]string
	ran      []string
}

func (r *replayRunner) Run(_ context.Context, cmd system.Command) (system.CommandResult, error) {
	r.t.Helper()
	line := cmd.String()
	r.ran = append(r.ran, line)
	name, ok := r.captures[line]
	if !ok {
		r.t.Errorf("adapter ran %q, which this test has no capture for", line)
		return system.CommandResult{ExitCode: -1}, errors.New("no capture")
	}
	read := func(ext string) string {
		b, err := os.ReadFile(fixture(r.set, name, ext))
		if err != nil {
			r.t.Fatalf("read capture: %v", err)
		}
		return string(b)
	}
	exit, err := strconv.Atoi(strings.TrimSpace(read("exit")))
	if err != nil {
		r.t.Fatalf("capture %s/%s: bad exit status: %v", r.set, name, err)
	}
	result := system.CommandResult{Stdout: read("stdout"), Stderr: read("stderr"), ExitCode: exit}
	if exit != 0 {
		// What os/exec reports for a non-zero exit, and so what the real
		// runner returns.
		return result, fmt.Errorf("exit status %d", exit)
	}
	return result, nil
}

var _ system.CommandRunner = (*replayRunner)(nil)

// readinessCaptures maps every command the readiness report runs to the
// capture holding its output on a regular (not induced) host set.
var readinessCaptures = map[string]string{
	"sysctl -n kern.racct.enable": "sysctl_racct",
	"sysctl -n hw.machine_arch":   "hw_machine_arch",
	"ifconfig -g bridge":          "ifconfig_bridge_group",
	"devfs rule showsets":         "devfs_rule_showsets",
	"dmesg":                       "dmesg",
	"pfctl -s info":               "pfctl_info",
}

// hostAdapter builds an adapter reading set's captured files: jail.conf (absent
// where cat failed), the devfs rules files and dmesg.boot.
func hostAdapter(t *testing.T, set string, captures map[string]string) (*Adapter, *replayRunner) {
	t.Helper()
	runner := &replayRunner{t: t, set: set, captures: captures}
	a := NewAdapter(runner)
	missing := filepath.Join(t.TempDir(), "absent")
	capturedFile := func(name string) string {
		b, err := os.ReadFile(fixture(set, name, "exit"))
		if err != nil {
			t.Fatalf("read capture: %v", err)
		}
		if strings.TrimSpace(string(b)) != "0" {
			return missing
		}
		return fixture(set, name, "stdout")
	}
	a.confFile = capturedFile("jail_conf")
	a.confDir = missing
	a.dmesgBoot = capturedFile("dmesg_boot")
	a.devfsRulesFiles = []string{capturedFile("devfs_rules_defaults"), capturedFile("devfs_rules_etc")}
	return a, runner
}

func TestSysctlFixtures(t *testing.T) {
	tests := []struct {
		set, capture, oid string
		want              string
		wantUnknownOID    bool
	}{
		{set: "vm-15.1", capture: "sysctl_racct", oid: "kern.racct.enable", want: "0"},
		{set: "isengard", capture: "sysctl_racct", oid: "kern.racct.enable", want: "1"},
		{set: "rohan", capture: "sysctl_racct", oid: "kern.racct.enable", want: "0"},
		// A kernel built without RACCT: the oid itself is missing.
		{set: "rivendell", capture: "sysctl_racct", oid: "kern.racct.enable", wantUnknownOID: true},
		{set: "vm-15.1", capture: "sysctl_racct_absent", oid: "kern.racct.nosuchknob", wantUnknownOID: true},
		{set: "vm-15.1", capture: "hw_machine_arch", oid: "hw.machine_arch", want: "amd64"},
		{set: "rivendell", capture: "hw_machine_arch", oid: "hw.machine_arch", want: "armv7"},
	}
	for _, tt := range tests {
		t.Run(tt.set+"/"+tt.capture, func(t *testing.T) {
			a, _ := hostAdapter(t, tt.set, map[string]string{"sysctl -n " + tt.oid: tt.capture})
			got, err := a.Sysctl(context.Background(), tt.oid)
			if tt.wantUnknownOID {
				if !errors.Is(err, host.ErrUnknownOID) {
					t.Fatalf("Sysctl() error = %v, want ErrUnknownOID", err)
				}
				if !strings.Contains(err.Error(), "unknown oid '"+tt.oid+"'") {
					t.Errorf("Sysctl() error = %q, want sysctl's own message in it", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Sysctl() = %q, %v; want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestJailConfDefaultsFixtures(t *testing.T) {
	// The VM has jail.conf with shared defaults; isengard and rivendell have
	// none, which is a nil map and no error.
	a, _ := hostAdapter(t, "vm-15.1", nil)
	defaults, err := a.JailConfDefaults(context.Background())
	if err != nil || defaults["exec.timeout"] != "120" {
		t.Errorf("vm-15.1: exec.timeout = %q, %v; want \"120\", nil", defaults["exec.timeout"], err)
	}
	for _, set := range []string{"isengard", "rivendell"} {
		a, _ := hostAdapter(t, set, nil)
		defaults, err := a.JailConfDefaults(context.Background())
		if err != nil || defaults != nil {
			t.Errorf("%s: JailConfDefaults() = %v, %v; want nil, nil", set, defaults, err)
		}
	}
}

func TestBridgesFixtures(t *testing.T) {
	tests := []struct {
		set, capture string
		want         []string
	}{
		{"vm-15.1", "ifconfig_bridge_group", []string{}},
		{"isengard", "ifconfig_bridge_group", []string{}},
		{"rohan", "ifconfig_bridge_group", []string{}},
		{"rivendell", "ifconfig_bridge_group", []string{}},
		{"vm-15.1-induced", "bridge_one_group", []string{"bridge0"}},
		{"isengard-induced", "bridge_two_group", []string{"bridge0", "bridge1"}},
	}
	for _, tt := range tests {
		t.Run(tt.set+"/"+tt.capture, func(t *testing.T) {
			runner := &replayRunner{t: t, set: tt.set, captures: map[string]string{"ifconfig -g bridge": tt.capture}}
			got, err := NewAdapter(runner).Bridges(context.Background())
			if err != nil || got == nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Bridges() = %#v, %v; want %#v, nil", got, err, tt.want)
			}
		})
	}
}

func TestDevfsRulesetsFixtures(t *testing.T) {
	// No host has /etc/devfs.rules, and every one has the five rulesets
	// /etc/defaults/devfs.rules ships — the Pi included.
	want := []host.DevfsRuleset{
		{Name: "devfsrules_hide_all", Number: 1},
		{Name: "devfsrules_unhide_basic", Number: 2},
		{Name: "devfsrules_unhide_login", Number: 3},
		{Name: "devfsrules_jail", Number: 4},
		{Name: "devfsrules_jail_vnet", Number: 5},
	}
	for _, set := range []string{"vm-15.1", "isengard", "rohan", "rivendell"} {
		t.Run(set, func(t *testing.T) {
			a, _ := hostAdapter(t, set, readinessCaptures)
			got, err := a.DevfsRulesets(context.Background())
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("DevfsRulesets() = %v, %v; want %v", got, err, want)
			}
		})
	}

	t.Run("numbers without names", func(t *testing.T) {
		a, _ := hostAdapter(t, "isengard", readinessCaptures)
		a.devfsRulesFiles = nil
		got, err := a.DevfsRulesets(context.Background())
		if err != nil || len(got) != 5 || got[3] != (host.DevfsRuleset{Number: 4}) {
			t.Errorf("DevfsRulesets() = %v, %v; want five unnamed rulesets", got, err)
		}
	})

	t.Run("unprivileged is an error, not none", func(t *testing.T) {
		a, _ := hostAdapter(t, "isengard", map[string]string{"devfs rule showsets": "devfs_rule_showsets_unpriv"})
		got, err := a.DevfsRulesets(context.Background())
		if err == nil || got != nil {
			t.Fatalf("DevfsRulesets() = %v, %v; want an error", got, err)
		}
		if !strings.Contains(err.Error(), "Operation not permitted") {
			t.Errorf("error = %q, want devfs's own message", err)
		}
	})
}

func TestCPUVirtualizationFixtures(t *testing.T) {
	bareMetal := []string{"PAT", "HLT", "MTF", "PAUSE", "EPT", "UG", "VPID"}
	tests := []struct {
		name, set string
		fromDmesg bool // dmesg.boot absent, so the live buffer is read
		want      host.CPUVirtualization
	}{
		// The KVM guest reports more than either bare-metal host.
		{name: "nested", set: "vm-15.1", want: host.CPUVirtualization{Reported: true,
			Features: []string{"PAT", "HLT", "MTF", "PAUSE", "EPT", "UG", "VPID", "VID", "PostIntr"}}},
		{name: "westmere", set: "isengard", want: host.CPUVirtualization{Reported: true, Features: bareMetal}},
		{name: "haswell", set: "rohan", want: host.CPUVirtualization{Reported: true, Features: bareMetal}},
		// armv7: no line at all, and an empty "CPU Features:" line is no match.
		{name: "arm", set: "rivendell", want: host.CPUVirtualization{}},
		// The fallback, on a buffer that still holds the line — next to sixteen
		// "root HUB" lines, none of which is a UG token.
		{name: "dmesg fallback", set: "isengard", fromDmesg: true,
			want: host.CPUVirtualization{Reported: true, Features: bareMetal}},
		// Up 83 days: the live buffer has wrapped and the line is gone.
		{name: "wrapped buffer", set: "rohan", fromDmesg: true, want: host.CPUVirtualization{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, runner := hostAdapter(t, tt.set, readinessCaptures)
			if tt.fromDmesg {
				a.dmesgBoot = filepath.Join(t.TempDir(), "absent")
			}
			got, err := a.CPUVirtualization(context.Background())
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CPUVirtualization() = %+v, %v; want %+v", got, err, tt.want)
			}
			ranDmesg := len(runner.ran) == 1 && runner.ran[0] == "dmesg"
			if ranDmesg != tt.fromDmesg {
				t.Errorf("ran %v; want dmesg only when dmesg.boot is absent", runner.ran)
			}
		})
	}
}

func TestPFStatusFixtures(t *testing.T) {
	tests := []struct {
		set, capture string
		want         host.PFStatus
	}{
		{"vm-15.1", "pfctl_info", host.PFStatus{}},
		{"isengard", "pfctl_info", host.PFStatus{}},
		{"rohan", "pfctl_info", host.PFStatus{}},
		{"rivendell", "pfctl_info", host.PFStatus{}},
		{"vm-15.1-induced", "pf_loaded_pfctl_info", host.PFStatus{Loaded: true}},
		{"isengard-induced", "pf_loaded_pfctl_info", host.PFStatus{Loaded: true}},
		{"vm-15.1-induced", "pf_enabled_pfctl_info", host.PFStatus{Loaded: true, Enabled: true}},
		{"isengard-induced", "pf_enabled_pfctl_info", host.PFStatus{Loaded: true, Enabled: true}},
	}
	for _, tt := range tests {
		t.Run(tt.set+"/"+tt.capture, func(t *testing.T) {
			runner := &replayRunner{t: t, set: tt.set, captures: map[string]string{"pfctl -s info": tt.capture}}
			got, err := NewAdapter(runner).PFStatus(context.Background())
			if err != nil || got != tt.want {
				t.Errorf("PFStatus() = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

// No capture has pfctl failing for any reason other than a missing /dev/pf.
// These states are constructed, not host output: they pin that anything else
// is an error for the service to call unknown, never "not loaded".
func TestPFStatusConstructedFailures(t *testing.T) {
	tests := []struct {
		name   string
		result system.CommandResult
		err    error
	}{
		{"other stderr", system.CommandResult{ExitCode: 1, Stderr: "constructed: not a captured message"}, errors.New("exit status 1")},
		{"timeout", system.CommandResult{ExitCode: -1}, context.DeadlineExceeded},
		{"exit 0 without a Status field", system.CommandResult{Stdout: "constructed\n"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
				return tt.result, tt.err
			})
			if got, err := NewAdapter(runner).PFStatus(context.Background()); err == nil {
				t.Errorf("PFStatus() = %+v, nil; want an error", got)
			}
		})
	}
}

// readOnlyCommands is everything the readiness report may run. A command
// outside this list in the report's path is a write until proven otherwise.
var readOnlyCommands = map[string]bool{
	"sysctl -n kern.racct.enable": true,
	"sysctl -n hw.machine_arch":   true,
	"ifconfig -g bridge":          true,
	"devfs rule showsets":         true,
	"dmesg":                       true,
	"pfctl -s info":               true,
}

// TestReadinessAgainstHostFixtures runs the whole report over each captured
// host and compares it with the table in testdata/hostfacts/README.md.
func TestReadinessAgainstHostFixtures(t *testing.T) {
	tests := []struct {
		set       string
		noBootLog bool
		want      map[host.CheckID]host.CheckStatus
	}{
		{set: "vm-15.1", want: map[host.CheckID]host.CheckStatus{
			host.CheckRacct: host.CheckFail, host.CheckExecTimeout: host.CheckPass, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckPass, host.CheckPF: host.CheckFail,
		}},
		{set: "isengard", want: map[host.CheckID]host.CheckStatus{
			host.CheckRacct: host.CheckPass, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckPass, host.CheckPF: host.CheckFail,
		}},
		{set: "rohan", want: map[host.CheckID]host.CheckStatus{
			host.CheckRacct: host.CheckFail, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckPass, host.CheckPF: host.CheckFail,
		}},
		// Without dmesg.boot, rohan's wrapped buffer can only say unknown.
		{set: "rohan", noBootLog: true, want: map[host.CheckID]host.CheckStatus{
			host.CheckRacct: host.CheckFail, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckUnknown, host.CheckPF: host.CheckFail,
		}},
		{set: "rivendell", want: map[host.CheckID]host.CheckStatus{
			host.CheckRacct: host.CheckUnknown, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckUnknown, host.CheckPF: host.CheckFail,
		}},
	}
	for _, tt := range tests {
		name := tt.set
		if tt.noBootLog {
			name += " without dmesg.boot"
		}
		t.Run(name, func(t *testing.T) {
			a, runner := hostAdapter(t, tt.set, readinessCaptures)
			if tt.noBootLog {
				a.dmesgBoot = filepath.Join(t.TempDir(), "absent")
			}
			report, err := host.NewService(a).Readiness(context.Background())
			if err != nil {
				t.Fatalf("Readiness() error = %v", err)
			}
			got := map[host.CheckID]host.CheckStatus{}
			for _, c := range report.Checks {
				got[c.ID] = c.Status
				if c.Status == host.CheckFail && c.Remedy == "" {
					t.Errorf("%s failed with no remedy", c.ID)
				}
				if c.Status != host.CheckFail && c.Remedy != "" {
					t.Errorf("%s is %s but carries remedy %q", c.ID, c.Status, c.Remedy)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("statuses = %v\nwant       %v", got, tt.want)
			}
			for _, line := range runner.ran {
				if !readOnlyCommands[line] {
					t.Errorf("readiness ran %q, which is not on the read-only list", line)
				}
			}
		})
	}
}
