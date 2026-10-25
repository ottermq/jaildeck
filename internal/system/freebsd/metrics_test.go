package freebsd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/metrics"
	"github.com/ottermq/jaildeck/internal/system"
)

// metricsAdapter answers each command with what the test set up and records
// what was asked.
type metricsAdapter struct {
	*Adapter
	ran    []string
	answer map[string]system.CommandResult
}

func newMetricsAdapter(t *testing.T) *metricsAdapter {
	t.Helper()
	m := &metricsAdapter{answer: map[string]system.CommandResult{}}
	m.Adapter = NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		line := cmd.String()
		m.ran = append(m.ran, line)
		result, known := m.answer[line]
		if !known {
			return system.CommandResult{ExitCode: 1, Stderr: "unexpected: " + line},
				errors.New("exit status 1")
		}
		if result.ExitCode != 0 {
			return result, errors.New("exit status 1")
		}
		return result, nil
	}))
	return m
}

func (m *metricsAdapter) sysctl(name, value string) {
	m.answer["sysctl -n "+name] = system.CommandResult{Stdout: value + "\n"}
}

// --- the host ----------------------------------------------------------

func TestLoadAverage(t *testing.T) {
	adapter := newMetricsAdapter(t)
	// sysctl prints it braced, which is the form that has to be unwrapped.
	adapter.sysctl("vm.loadavg", "{ 0.42 0.31 0.18 }")

	got, err := adapter.LoadAverage(context.Background())
	if err != nil {
		t.Fatalf("LoadAverage: %v", err)
	}
	want := metrics.LoadAverage{One: 0.42, Five: 0.31, Fifteen: 0.18}
	if got != want {
		t.Errorf("load = %+v, want %+v", got, want)
	}
}

func TestLoadAverageRefusesWhatItCannotRead(t *testing.T) {
	for _, value := range []string{"{ 0.42 0.31 }", "{ }", "{ a b c }", ""} {
		adapter := newMetricsAdapter(t)
		adapter.sysctl("vm.loadavg", value)

		if _, err := adapter.LoadAverage(context.Background()); err == nil {
			t.Errorf("LoadAverage accepted %q", value)
		}
	}
}

func TestCPUTicks(t *testing.T) {
	adapter := newMetricsAdapter(t)
	// user nice system interrupt idle, in that order and on one line.
	adapter.sysctl("kern.cp_time", "226572 155 847113 47482 677314310")
	adapter.sysctl("hw.ncpu", "16")

	ticks, cpus, err := adapter.CPUTicks(context.Background())
	if err != nil {
		t.Fatalf("CPUTicks: %v", err)
	}
	want := metrics.Ticks{User: 226572, Nice: 155, System: 847113,
		Interrupt: 47482, Idle: 677314310}
	if ticks != want {
		t.Errorf("ticks = %+v, want %+v", ticks, want)
	}
	if cpus != 16 {
		t.Errorf("cpus = %d, want 16", cpus)
	}
}

// Five counters, and a line that is not five of them is not a reading.
func TestCPUTicksRefusesTheWrongShape(t *testing.T) {
	for _, value := range []string{"1 2 3 4", "1 2 3 4 5 6", "a b c d e", ""} {
		adapter := newMetricsAdapter(t)
		adapter.sysctl("kern.cp_time", value)
		adapter.sysctl("hw.ncpu", "16")

		if _, _, err := adapter.CPUTicks(context.Background()); err == nil {
			t.Errorf("CPUTicks accepted %q", value)
		}
	}
}

func TestMemory(t *testing.T) {
	adapter := newMetricsAdapter(t)
	adapter.sysctl("hw.pagesize", "4096")
	adapter.sysctl("hw.physmem", "17120813056")
	adapter.sysctl("vm.stats.vm.v_free_count", "3111566")
	adapter.sysctl("vm.stats.vm.v_active_count", "10270")
	adapter.sysctl("vm.stats.vm.v_inactive_count", "106011")
	adapter.sysctl("vm.stats.vm.v_wire_count", "845271")

	got, err := adapter.Memory(context.Background())
	if err != nil {
		t.Fatalf("Memory: %v", err)
	}
	want := metrics.Memory{
		Physical: 17120813056,
		Free:     3111566 * 4096,
		Active:   10270 * 4096,
		Inactive: 106011 * 4096,
		Wired:    845271 * 4096,
	}
	if got != want {
		t.Errorf("memory = %+v, want %+v", got, want)
	}
	// v_cache_count exists on this release and is always 0 — a leftover of a
	// page queue FreeBSD no longer keeps. Reading it would be reporting a
	// number that means nothing.
	for _, ran := range adapter.ran {
		if strings.Contains(ran, "v_cache_count") {
			t.Error("read v_cache_count, which is vestigial")
		}
	}
}

func TestPoolUsage(t *testing.T) {
	adapter := newMetricsAdapter(t)
	const cmd = "zpool list -Hp -o name,size,alloc,free,capacity,health"
	adapter.answer[cmd] = system.CommandResult{Stdout: strings.Join([]string{
		"backup\t498216206336\t9126301696\t489089904640\t1\tONLINE",
		"tank\t996432412672\t2390806528\t994041606144\t0\tONLINE",
	}, "\n") + "\n"}

	pools, err := adapter.PoolUsage(context.Background())
	if err != nil {
		t.Fatalf("PoolUsage: %v", err)
	}
	want := []metrics.Pool{
		{Name: "backup", Size: 498216206336, Allocated: 9126301696,
			Free: 489089904640, Capacity: 1, Health: "ONLINE"},
		{Name: "tank", Size: 996432412672, Allocated: 2390806528,
			Free: 994041606144, Capacity: 0, Health: "ONLINE"},
	}
	if !reflect.DeepEqual(pools, want) {
		t.Errorf("pools = %+v, want %+v", pools, want)
	}
}

// A host without ZFS does not lack the binary: zpool exits non-zero. That is
// no pools, which is an answer — the same position the layout check takes.
func TestPoolUsageOnAHostWithoutZFS(t *testing.T) {
	adapter := newMetricsAdapter(t)
	adapter.answer["zpool list -Hp -o name,size,alloc,free,capacity,health"] =
		system.CommandResult{ExitCode: 1, Stderr: "no such file or directory"}

	pools, err := adapter.PoolUsage(context.Background())
	if err != nil {
		t.Fatalf("PoolUsage: %v", err)
	}
	if len(pools) != 0 {
		t.Errorf("pools = %+v, want none", pools)
	}
	if pools == nil {
		t.Error("pools is nil, which marshals to null rather than []")
	}
}

// --- per jail ----------------------------------------------------------

func TestJailUsageFromRctl(t *testing.T) {
	adapter := newMetricsAdapter(t)
	adapter.answer["rctl -u jail:postgres"] = system.CommandResult{Stdout: strings.Join([]string{
		"cputime=226", "datasize=3506176", "memoryuse=290263040", "maxproc=13",
		"openfiles=9256", "vmemoryuse=1865551872", "nthr=13", "pcpu=12",
	}, "\n") + "\n"}

	usage, err := adapter.JailUsage(context.Background(), "postgres")
	if err != nil {
		t.Fatalf("JailUsage: %v", err)
	}
	want := metrics.JailUsage{
		Source: metrics.SourceRctl, PCPU: 12, MemoryUse: 290263040, Processes: 13,
		VMemoryUse: 1865551872, OpenFiles: 9256, CPUTime: 226,
	}
	if usage != want {
		t.Errorf("usage = %+v, want %+v", usage, want)
	}
}

// maxproc in a *usage* reading is the current process count, not a limit.
func TestJailUsageReadsMaxprocAsTheProcessCount(t *testing.T) {
	adapter := newMetricsAdapter(t)
	adapter.answer["rctl -u jail:db1"] = system.CommandResult{Stdout: "maxproc=7\n"}

	usage, err := adapter.JailUsage(context.Background(), "db1")
	if err != nil {
		t.Fatalf("JailUsage: %v", err)
	}
	if usage.Processes != 7 {
		t.Errorf("processes = %d, want 7", usage.Processes)
	}
}

func TestJailProcessUsageSumsTheJailsProcesses(t *testing.T) {
	adapter := newMetricsAdapter(t)
	// Two -o flags, not one comma-separated list: `-o rss=,pcpu=` suppresses
	// only the first header and prints ",pcpu=" as the second. Measured.
	adapter.answer["ps -J db1 -o rss= -o pcpu="] = system.CommandResult{
		Stdout: "  3196 0.0\n  3016 1.5\n 29748 0.5\n",
	}

	usage, err := adapter.JailProcessUsage(context.Background(), "db1")
	if err != nil {
		t.Fatalf("JailProcessUsage: %v", err)
	}
	if usage.Source != metrics.SourcePS {
		t.Errorf("source = %q, want %q", usage.Source, metrics.SourcePS)
	}
	// ps reports rss in kibibytes; the API is exact bytes throughout.
	if want := int64((3196 + 3016 + 29748) * 1024); usage.MemoryUse != want {
		t.Errorf("memoryuse = %d, want %d", usage.MemoryUse, want)
	}
	if usage.PCPU != 2.0 {
		t.Errorf("pcpu = %v, want 2.0", usage.PCPU)
	}
	if usage.Processes != 3 {
		t.Errorf("processes = %d, want 3", usage.Processes)
	}
	// What ps cannot answer stays absent rather than reading as a zero
	// somebody measured.
	if usage.VMemoryUse != 0 || usage.OpenFiles != 0 || usage.CPUTime != 0 {
		t.Errorf("usage carries rctl-only fields: %+v", usage)
	}
}

// A running jail with nothing in it: ps exits 1 with nothing on stdout, and
// that is an answer rather than a failure.
func TestJailProcessUsageOfAnEmptyJail(t *testing.T) {
	adapter := newMetricsAdapter(t)
	adapter.answer["ps -J db1 -o rss= -o pcpu="] = system.CommandResult{ExitCode: 1}

	usage, err := adapter.JailProcessUsage(context.Background(), "db1")
	if err != nil {
		t.Fatalf("JailProcessUsage: %v", err)
	}
	if usage.Processes != 0 || usage.MemoryUse != 0 {
		t.Errorf("usage = %+v, want nothing", usage)
	}
	if usage.Source != metrics.SourcePS {
		t.Errorf("source = %q", usage.Source)
	}
}

// A jail that stopped between the listing and this read is not an empty
// jail, and the two differ only in what ps says about them.
func TestJailProcessUsageOfAJailThatIsGone(t *testing.T) {
	adapter := newMetricsAdapter(t)
	adapter.answer["ps -J db1 -o rss= -o pcpu="] = system.CommandResult{
		ExitCode: 1, Stderr: "ps: Invalid jail id: db1",
	}

	if _, err := adapter.JailProcessUsage(context.Background(), "db1"); err == nil {
		t.Fatal("want the error: a jail that is not there is not a jail with no processes")
	}
}

// The name reaches here from the jail listing, but it is still the one place
// it becomes an argument to a command that takes it bare.
func TestJailMetricsRefuseANameThatIsNotOne(t *testing.T) {
	for _, name := range []string{"", "db1 db2", "jail:db1", "-J", "-rf"} {
		adapter := newMetricsAdapter(t)

		if _, err := adapter.JailUsage(context.Background(), name); err == nil {
			t.Errorf("JailUsage accepted %q", name)
		}
		if _, err := adapter.JailProcessUsage(context.Background(), name); err == nil {
			t.Errorf("JailProcessUsage accepted %q", name)
		}
		if len(adapter.ran) != 0 {
			t.Errorf("%q reached the host as %v", name, adapter.ran)
		}
	}
}

// --- the tick rate (JD-033) --------------------------------------------

// The exact line isengard prints. stathz is 127 and hz is 1000, and
// kern.cp_time counts in the first: measured, the counters advance about 127
// times a second per CPU. Dividing by hz would report CPU seconds eight times
// too small, and nothing about the number would look wrong.
func TestTickRateIsStathzAndNotHz(t *testing.T) {
	adapter := newMetricsAdapter(t)
	adapter.sysctl("kern.clockrate", "{ hz = 1000, tick = 1000, profhz = 8128, stathz = 127 }")

	rate, err := adapter.TickRate(context.Background())
	if err != nil {
		t.Fatalf("TickRate: %v", err)
	}
	if rate != 127 {
		t.Errorf("rate = %d, want 127 (stathz); 1000 would be hz", rate)
	}
}

// stathz may legitimately be zero, which means the statistics clock is the
// main one. hz is the rate then, rather than an error or a zero that would
// divide by nothing.
func TestTickRateFallsBackToHzWhenStathzIsZero(t *testing.T) {
	adapter := newMetricsAdapter(t)
	adapter.sysctl("kern.clockrate", "{ hz = 100, tick = 10000, profhz = 8128, stathz = 0 }")

	rate, err := adapter.TickRate(context.Background())
	if err != nil {
		t.Fatalf("TickRate: %v", err)
	}
	if rate != 100 {
		t.Errorf("rate = %d, want 100 (hz, because stathz is 0)", rate)
	}
}

func TestTickRateRefusesALineWithNeither(t *testing.T) {
	for _, value := range []string{"", "{ }", "{ tick = 1000 }", "nonsense"} {
		adapter := newMetricsAdapter(t)
		adapter.sysctl("kern.clockrate", value)
		if _, err := adapter.TickRate(context.Background()); err == nil {
			t.Errorf("TickRate(%q) returned no error", value)
		}
	}
}

// Read as name = number pairs, never by position. The braces, the spacing and
// the set of fields are a printf in the kernel, and a release that adds one
// would silently shift a positional read onto the wrong number.
func TestClockRatesReadsPairsNotPositions(t *testing.T) {
	// Same four values, reordered, with a new field in front.
	rates := clockRates("{ newfield = 7, stathz = 127, hz = 1000, tick = 1000, profhz = 8128 }")
	for name, want := range map[string]int{
		"newfield": 7, "stathz": 127, "hz": 1000, "tick": 1000, "profhz": 8128,
	} {
		if rates[name] != want {
			t.Errorf("%s = %d, want %d", name, rates[name], want)
		}
	}
}
