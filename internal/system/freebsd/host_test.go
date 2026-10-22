package freebsd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/audit"
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
	return replay(r.t, r.set, name)
}

// replay returns one capture as the real runner would have returned it.
func replay(t *testing.T, set, name string) (system.CommandResult, error) {
	t.Helper()
	read := func(ext string) string {
		b, err := os.ReadFile(fixture(set, name, ext))
		if err != nil {
			t.Fatalf("read capture: %v", err)
		}
		return string(b)
	}
	exit, err := strconv.Atoi(strings.TrimSpace(read("exit")))
	if err != nil {
		t.Fatalf("capture %s/%s: bad exit status: %v", set, name, err)
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
	"zpool list -H -o name":       "zpool_list_h",
}

// withPools adds the zfs list -r captures a set has for each pool named.
func withPools(captures map[string]string, pools ...string) map[string]string {
	all := make(map[string]string, len(captures)+len(pools))
	for k, v := range captures {
		all[k] = v
	}
	for _, pool := range pools {
		all["zfs list -H -o name,mountpoint -r "+pool] = "zfs_list_r_" + pool
	}
	return all
}

// setPools is every pool set has a zfs list -r capture for.
func setPools(t *testing.T, set string) []string {
	t.Helper()
	matches, err := filepath.Glob(fixture(set, "zfs_list_r_*", "exit"))
	if err != nil {
		t.Fatal(err)
	}
	pools := make([]string, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "zfs_list_r_"), ".exit")
		if !strings.Contains(name, "_") { // zfs_list_r_zroot_jails lists a dataset, not a pool
			pools = append(pools, name)
		}
	}
	return pools
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
	"zpool list -H -o name":       true,
}

func readOnly(line string) bool {
	return readOnlyCommands[line] || strings.HasPrefix(line, "zfs list -H -o name,mountpoint -r ")
}

// TestReadinessAgainstHostFixtures runs the whole report over each captured
// host and compares it with the table in testdata/hostfacts/README.md.
func TestReadinessAgainstHostFixtures(t *testing.T) {
	tests := []struct {
		set       string
		pool      string // JAILDECK_ZFS_POOL
		noBootLog bool
		want      map[host.CheckID]host.CheckStatus
	}{
		{set: "vm-15.1", want: map[host.CheckID]host.CheckStatus{
			host.CheckZFSPool: host.CheckPass, host.CheckZFSLayout: host.CheckPass,
			host.CheckRacct: host.CheckFail, host.CheckExecTimeout: host.CheckPass, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckPass, host.CheckPF: host.CheckFail,
		}},
		// Three pools and none configured: the report will not pick one.
		{set: "isengard", want: map[host.CheckID]host.CheckStatus{
			host.CheckZFSPool: host.CheckFail, host.CheckZFSLayout: host.CheckUnknown,
			host.CheckRacct: host.CheckPass, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckPass, host.CheckPF: host.CheckFail,
		}},
		{set: "isengard", pool: "tank", want: map[host.CheckID]host.CheckStatus{
			host.CheckZFSPool: host.CheckPass, host.CheckZFSLayout: host.CheckPass,
			host.CheckRacct: host.CheckPass, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckPass, host.CheckPF: host.CheckFail,
		}},
		// backup holds a replica of tank's layout, but not at backup/jails.
		{set: "isengard", pool: "backup", want: map[host.CheckID]host.CheckStatus{
			host.CheckZFSPool: host.CheckPass, host.CheckZFSLayout: host.CheckFail,
			host.CheckRacct: host.CheckPass, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckPass, host.CheckPF: host.CheckFail,
		}},
		{set: "rohan", pool: "zroot", want: map[host.CheckID]host.CheckStatus{
			host.CheckZFSPool: host.CheckPass, host.CheckZFSLayout: host.CheckFail,
			host.CheckRacct: host.CheckFail, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckPass, host.CheckPF: host.CheckFail,
		}},
		// Without dmesg.boot, rohan's wrapped buffer can only say unknown.
		{set: "rohan", pool: "zroot", noBootLog: true, want: map[host.CheckID]host.CheckStatus{
			host.CheckZFSPool: host.CheckPass, host.CheckZFSLayout: host.CheckFail,
			host.CheckRacct: host.CheckFail, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckUnknown, host.CheckPF: host.CheckFail,
		}},
		// No ZFS at all: zpool exists and fails.
		{set: "rivendell", want: map[host.CheckID]host.CheckStatus{
			host.CheckZFSPool: host.CheckFail, host.CheckZFSLayout: host.CheckUnknown,
			host.CheckRacct: host.CheckUnknown, host.CheckExecTimeout: host.CheckFail, host.CheckBridges: host.CheckFail,
			host.CheckDevfsRulesets: host.CheckPass, host.CheckCPUVirtualization: host.CheckUnknown, host.CheckPF: host.CheckFail,
		}},
	}
	for _, tt := range tests {
		name := tt.set
		if tt.pool != "" {
			name += " pool " + tt.pool
		}
		if tt.noBootLog {
			name += " without dmesg.boot"
		}
		t.Run(name, func(t *testing.T) {
			a, runner := hostAdapter(t, tt.set, withPools(readinessCaptures, setPools(t, tt.set)...))
			if tt.noBootLog {
				a.dmesgBoot = filepath.Join(t.TempDir(), "absent")
			}
			report, err := host.NewService(a, nil, tt.pool).Readiness(context.Background())
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
				if !readOnly(line) {
					t.Errorf("readiness ran %q, which is not on the read-only list", line)
				}
			}
		})
	}
}

func TestPoolsFixtures(t *testing.T) {
	tests := []struct {
		set  string
		want []string
	}{
		{"vm-15.1", []string{"zroot"}},
		{"isengard", []string{"backup", "tank", "zroot"}},
		{"rohan", []string{"media", "zroot"}},
	}
	for _, tt := range tests {
		t.Run(tt.set, func(t *testing.T) {
			runner := &replayRunner{t: t, set: tt.set, captures: readinessCaptures}
			got, err := NewAdapter(runner).Pools(context.Background())
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Pools() = %v, %v; want %v", got, err, tt.want)
			}
		})
	}

	// No ZFS: zpool is there and exits 1. The service needs the exit status to
	// tell that from a timeout, so it must survive as a CommandError.
	t.Run("rivendell", func(t *testing.T) {
		runner := &replayRunner{t: t, set: "rivendell", captures: readinessCaptures}
		got, err := NewAdapter(runner).Pools(context.Background())
		var cmdErr *system.CommandError
		if got != nil || !errors.As(err, &cmdErr) || cmdErr.Result.ExitCode != 1 {
			t.Fatalf("Pools() = %v, %v; want a CommandError with exit 1", got, err)
		}
		if !strings.Contains(err.Error(), "Failed to load zfs module") {
			t.Errorf("error = %q, want zpool's own message", err)
		}
	})
}

func TestDatasetsFixtures(t *testing.T) {
	tests := []struct {
		set, pool string
		count     int
		want      host.Dataset // one line the parse must have produced
	}{
		{"vm-15.1", "zroot", 21, host.Dataset{Name: "zroot/jails", Mountpoint: "/usr/local/jails"}},
		{"vm-15.1", "zroot", 21, host.Dataset{Name: "zroot", Mountpoint: "none"}},
		{"isengard", "tank", 9, host.Dataset{Name: "tank/jails/media", Mountpoint: "/usr/local/jails/media"}},
		{"isengard", "backup", 23, host.Dataset{Name: "backup/isengard/tank/jails", Mountpoint: "none"}},
		{"isengard", "zroot", 15, host.Dataset{Name: "zroot", Mountpoint: "/zroot"}},
		{"rohan", "media", 7, host.Dataset{Name: "media/.incoming", Mountpoint: "/srv/media/.incoming"}},
		{"rohan", "zroot", 20, host.Dataset{Name: "zroot/srv", Mountpoint: "/srv"}},
	}
	for _, tt := range tests {
		t.Run(tt.set+"/"+tt.pool, func(t *testing.T) {
			runner := &replayRunner{t: t, set: tt.set, captures: withPools(nil, tt.pool)}
			got, err := NewAdapter(runner).Datasets(context.Background(), tt.pool)
			if err != nil || len(got) != tt.count || !slices.Contains(got, tt.want) {
				t.Errorf("Datasets() = %d datasets, %v; want %d including %+v", len(got), err, tt.count, tt.want)
			}
		})
	}

	t.Run("pool gone", func(t *testing.T) {
		runner := &replayRunner{t: t, set: "vm-15.1",
			captures: map[string]string{"zfs list -H -o name,mountpoint -r nosuchpool": "zfs_list_absent_pool"}}
		if got, err := NewAdapter(runner).Datasets(context.Background(), "nosuchpool"); err == nil {
			t.Errorf("Datasets() = %v, nil; want an error", got)
		}
	})
}

// step is one command an ensure is expected to run, and the capture that
// answers it.
type step struct {
	command   string
	set, name string
}

// TestEnsureLayoutCommands drives the whole ensure over captured bytes, the
// writes included: vm-15.1-induced holds the layout partly there and moved
// aside, and what zfs create prints when it succeeds and when the dataset
// already exists (capture-layout-induced.sh).
func TestEnsureLayoutCommands(t *testing.T) {
	const zpool, zfsList = "zpool list -H -o name", "zfs list -H -o name,mountpoint -r zroot"
	reads := func(set, list string) []step {
		return []step{{zpool, "vm-15.1", "zpool_list_h"}, {zfsList, set, list}}
	}
	complete := reads("vm-15.1", "zfs_list_r_zroot")
	partial := reads("vm-15.1-induced", "layout_partial_zfs_list")
	createMedia := step{"zfs create zroot/jails/media", "vm-15.1-induced", "layout_zfs_create"}

	tests := []struct {
		name        string
		steps       []step
		wantCreated []string
		wantErr     string
	}{
		// Complete: the reads, and not one zfs create.
		{name: "complete", steps: complete, wantCreated: []string{}},
		// media missing: only media is created, bare, and the layout is read
		// again — now complete.
		{name: "partial", steps: slices.Concat(partial, []step{createMedia}, complete),
			wantCreated: []string{"zroot/jails/media"}},
		// media moved: refused before anything is created.
		{name: "misplaced", steps: reads("vm-15.1-induced", "layout_misplaced_zfs_list"),
			wantErr: "zroot/jails/media is mounted at /mnt/jd-capture-misplaced, expected /usr/local/jails/media"},
		// The dataset appeared between the read and the create.
		{name: "create races", steps: slices.Concat(partial,
			[]step{{"zfs create zroot/jails/media", "vm-15.1-induced", "layout_zfs_create_exists"}}),
			wantErr: "dataset already exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := 0
			runner := runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
				if ran >= len(tt.steps) {
					t.Fatalf("ran %q after the last expected command", cmd)
				}
				want := tt.steps[ran]
				ran++
				if got := cmd.String(); got != want.command {
					t.Fatalf("command %d = %q, want %q", ran, got, want.command)
				}
				return replay(t, want.set, want.name)
			})
			logger := audit.NewFileLogger(filepath.Join(t.TempDir(), "audit.log"))
			change, err := host.NewService(NewAdapter(runner), logger, "").EnsureLayout(context.Background())
			if ran != len(tt.steps) {
				t.Errorf("ran %d commands, want %d", ran, len(tt.steps))
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("EnsureLayout() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("EnsureLayout() error = %v", err)
			}
			if !slices.Equal(change.Created, tt.wantCreated) || len(change.Missing) != 0 {
				t.Errorf("change = %+v, want created %v and nothing missing", change, tt.wantCreated)
			}
		})
	}
}

// Captured on rohan, which has no jails dataset on either pool: the parent is
// created with the mountpoint and the children bare. The argument arrays are
// compared whole, since two arrays can join to the same string.
func TestEnsureLayoutCreatesParentFirst(t *testing.T) {
	var creates [][]string
	runner := runnerFunc(func(ctx context.Context, cmd system.Command) (system.CommandResult, error) {
		if cmd.Name == "zfs" && cmd.Args[0] == "create" {
			creates = append(creates, slices.Clone(cmd.Args))
			return replay(t, "vm-15.1-induced", "layout_zfs_create")
		}
		// zfs list -r media too: with the parent missing, the other pool is
		// checked for a dataset already mounted at /usr/local/jails.
		return (&replayRunner{t: t, set: "rohan", captures: withPools(readinessCaptures, "zroot", "media")}).Run(ctx, cmd)
	})
	logger := audit.NewFileLogger(filepath.Join(t.TempDir(), "audit.log"))
	if _, err := host.NewService(NewAdapter(runner), logger, "zroot").EnsureLayout(context.Background()); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	want := [][]string{
		{"create", "-o", "mountpoint=/usr/local/jails", "zroot/jails"},
		{"create", "zroot/jails/media"},
		{"create", "zroot/jails/templates"},
		{"create", "zroot/jails/containers"},
	}
	if !reflect.DeepEqual(creates, want) {
		t.Errorf("zfs args = %q, want %q", creates, want)
	}
	entries, err := logger.Recent(context.Background(), 0, audit.Filter{Operation: host.OperationCreateDataset})
	if err != nil || len(entries) != 4 {
		t.Errorf("audit entries = %d, %v; want one per zfs create", len(entries), err)
	}
}

// isengard with JAILDECK_ZFS_POOL=backup: backup/jails does not exist, and
// creating it would mount it over tank/jails, where the jails actually run.
// The replica under backup/isengard/tank is not at backup/jails, so this is
// not a misplaced dataset but mountpoints another pool already holds.
func TestEnsureLayoutRefusesToMountOverAnotherPool(t *testing.T) {
	newService := func(runner *replayRunner) *host.Service {
		logger := audit.NewFileLogger(filepath.Join(t.TempDir(), "audit.log"))
		return host.NewService(NewAdapter(runner), logger, "backup")
	}
	captures := withPools(readinessCaptures, setPools(t, "isengard")...)

	runner := &replayRunner{t: t, set: "isengard", captures: captures}
	_, err := newService(runner).EnsureLayout(context.Background())
	if err == nil || !strings.Contains(err.Error(), "backup/jails would be mounted at /usr/local/jails, which tank/jails already holds") {
		t.Fatalf("EnsureLayout() error = %v, want a refusal naming tank/jails", err)
	}
	for _, line := range runner.ran {
		if !readOnly(line) {
			t.Errorf("ran %q before refusing", line)
		}
	}

	a, _ := hostAdapter(t, "isengard", captures)
	report, err := host.NewService(a, nil, "backup").Readiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Checks {
		if c.ID != host.CheckZFSLayout {
			continue
		}
		// The remedy must not be jaildeck init, which is what would do it.
		if c.Status != host.CheckFail || !strings.HasPrefix(c.Observed, "backup/jails would be mounted at /usr/local/jails, which tank/jails already holds") ||
			!strings.Contains(c.Remedy, "one of: tank") {
			t.Errorf("zfs_layout = %+v", c)
		}
	}
}
