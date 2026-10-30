package freebsd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/metrics"
	"github.com/ottermq/jaildeck/internal/system"
)

// metricsTimeout bounds a reading. Each one is a sysctl or a single short
// command (JD-014).
const metricsTimeout = 15 * time.Second

// LoadAverage reads vm.loadavg, which sysctl prints as "{ 0.12 0.08 0.02 }".
func (a *Adapter) LoadAverage(ctx context.Context) (metrics.LoadAverage, error) {
	value, err := a.Sysctl(ctx, "vm.loadavg")
	if err != nil {
		return metrics.LoadAverage{}, err
	}
	fields := strings.Fields(strings.Trim(strings.TrimSpace(value), "{} "))
	if len(fields) != 3 {
		return metrics.LoadAverage{}, fmt.Errorf("parse vm.loadavg: %q is not three averages", value)
	}
	numbers := make([]float64, 3)
	for i, field := range fields {
		parsed, err := strconv.ParseFloat(field, 64)
		if err != nil {
			return metrics.LoadAverage{}, fmt.Errorf("parse vm.loadavg: %q is not a number", field)
		}
		numbers[i] = parsed
	}
	return metrics.LoadAverage{One: numbers[0], Five: numbers[1], Fifteen: numbers[2]}, nil
}

// CPUTicks reads kern.cp_time, five cumulative counters printed on one line
// in the order user, nice, system, interrupt, idle -- and hw.ncpu, which is
// how many CPUs those counters are aggregated across.
func (a *Adapter) CPUTicks(ctx context.Context) (metrics.Ticks, int, error) {
	value, err := a.Sysctl(ctx, "kern.cp_time")
	if err != nil {
		return metrics.Ticks{}, 0, err
	}
	fields := strings.Fields(value)
	if len(fields) != 5 {
		return metrics.Ticks{}, 0, fmt.Errorf(
			"parse kern.cp_time: %q is not five counters", value)
	}
	counters := make([]uint64, 5)
	for i, field := range fields {
		parsed, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return metrics.Ticks{}, 0, fmt.Errorf("parse kern.cp_time: %q is not a counter", field)
		}
		counters[i] = parsed
	}
	cpus, err := a.sysctlInt(ctx, "hw.ncpu")
	if err != nil {
		return metrics.Ticks{}, 0, err
	}
	return metrics.Ticks{
		User: counters[0], Nice: counters[1], System: counters[2],
		Interrupt: counters[3], Idle: counters[4],
	}, int(cpus), nil
}

// TickRate is how many kern.cp_time ticks make one second.
//
// It is kern.clockrate's **stathz**, not its hz, and the difference is not
// cosmetic. Measured on isengard: `{ hz = 1000, tick = 1000, profhz = 8128,
// stathz = 127 }`, and over two seconds the counters advanced 3995 ticks
// across 16 CPUs -- 125 per CPU per second, which is stathz. Dividing by hz
// would report CPU seconds eight times too small, and nothing about the
// number would look wrong (JD-033).
//
// stathz may legitimately be 0, which means the statistics clock is the main
// one; hz is the rate then.
func (a *Adapter) TickRate(ctx context.Context) (int, error) {
	value, err := a.Sysctl(ctx, "kern.clockrate")
	if err != nil {
		return 0, err
	}
	rates := clockRates(value)
	if stathz := rates["stathz"]; stathz > 0 {
		return stathz, nil
	}
	if hz := rates["hz"]; hz > 0 {
		return hz, nil
	}
	return 0, fmt.Errorf("parse kern.clockrate: %q carries neither stathz nor hz", value)
}

// clockRates pulls the name = number pairs out of
// "{ hz = 1000, tick = 1000, profhz = 8128, stathz = 127 }".
//
// Read as pairs rather than by position: the braces, the spacing and the set
// of names are a printf in the kernel, and a release that adds a field would
// silently shift any positional read onto the wrong number.
func clockRates(value string) map[string]int {
	rates := map[string]int{}
	for _, pair := range strings.Split(strings.Trim(strings.TrimSpace(value), "{}"), ",") {
		name, number, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(number))
		if err != nil {
			continue
		}
		rates[strings.TrimSpace(name)] = parsed
	}
	return rates
}

// Memory reads the page counters and multiplies them by the page size.
//
// v_cache_count is not read: it exists on this release and is always 0, a
// leftover of a page queue FreeBSD no longer keeps.
func (a *Adapter) Memory(ctx context.Context) (metrics.Memory, error) {
	pageSize, err := a.sysctlInt(ctx, "hw.pagesize")
	if err != nil {
		return metrics.Memory{}, err
	}
	physical, err := a.sysctlInt(ctx, "hw.physmem")
	if err != nil {
		return metrics.Memory{}, err
	}

	pages := map[string]int64{}
	for _, name := range []string{
		"vm.stats.vm.v_free_count",
		"vm.stats.vm.v_active_count",
		"vm.stats.vm.v_inactive_count",
		"vm.stats.vm.v_wire_count",
	} {
		count, err := a.sysctlInt(ctx, name)
		if err != nil {
			return metrics.Memory{}, err
		}
		pages[name] = count * pageSize
	}
	return metrics.Memory{
		Physical: physical,
		Free:     pages["vm.stats.vm.v_free_count"],
		Active:   pages["vm.stats.vm.v_active_count"],
		Inactive: pages["vm.stats.vm.v_inactive_count"],
		Wired:    pages["vm.stats.vm.v_wire_count"],
	}, nil
}

// Pools reads `zpool list -Hp`: exact bytes rather than the "1.41G" the
// default output prints, which cannot be compared or totalled (JD-022).
//
// A host without ZFS has no pools, which is an answer: zpool exits non-zero
// and that is reported as no pools rather than as a failed request, the same
// position the layout check takes (JD-021).
func (a *Adapter) PoolUsage(ctx context.Context) ([]metrics.Pool, error) {
	result, err := a.runHost(ctx, system.Command{
		Name:    "zpool",
		Args:    []string{"list", "-Hp", "-o", "name,size,alloc,free,capacity,health"},
		Timeout: metricsTimeout,
	})
	if err != nil {
		if exited(result, err) {
			return []metrics.Pool{}, nil
		}
		return nil, err
	}

	pools := make([]metrics.Pool, 0)
	for _, line := range nonEmptyLines(result.Stdout) {
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			return nil, fmt.Errorf("parse zpool list: %q has %d fields, want 6", line, len(fields))
		}
		pool := metrics.Pool{Name: fields[0], Health: fields[5]}
		for i, into := range []*int64{&pool.Size, &pool.Allocated, &pool.Free} {
			parsed, err := strconv.ParseInt(fields[i+1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse zpool list: %q is not a byte count", fields[i+1])
			}
			*into = parsed
		}
		capacity, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, fmt.Errorf("parse zpool list: %q is not a percentage", fields[4])
		}
		pool.Capacity = capacity
		pools = append(pools, pool)
	}
	return pools, nil
}

// JailUsage reads `rctl -u jail:<name>`, which prints `key=value` a line at
// a time.
//
// Every field is read even though only a few are reported, because the cost
// is one command either way and a field the report does not carry today is
// one line to add rather than another round trip.
func (a *Adapter) JailUsage(ctx context.Context, name string) (metrics.JailUsage, error) {
	fields, err := a.rctlUsage(ctx, name)
	if err != nil {
		return metrics.JailUsage{}, err
	}
	return metrics.JailUsage{
		Source:     metrics.SourceRctl,
		PCPU:       float64(fields["pcpu"]),
		MemoryUse:  fields["memoryuse"],
		Processes:  int(fields["maxproc"]),
		VMemoryUse: fields["vmemoryuse"],
		OpenFiles:  fields["openfiles"],
		CPUTime:    fields["cputime"],
	}, nil
}

// rctlUsage reads `rctl -u jail:<name>` into its fields. Shared by the
// metrics report and by a limits change, which compares a lowered limit with
// what the jail uses.
func (a *Adapter) rctlUsage(ctx context.Context, name string) (map[string]int64, error) {
	subject, err := rctlSubject(name)
	if err != nil {
		return nil, err
	}
	result, err := a.runHost(ctx, system.Command{
		Name:    "rctl",
		Args:    []string{"-u", subject},
		Timeout: rctlTimeout,
	})
	if err != nil {
		return nil, err
	}

	fields := map[string]int64{}
	for _, line := range nonEmptyLines(result.Stdout) {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		fields[strings.TrimSpace(key)] = parsed
	}
	return fields, nil
}

// JailProcessUsage sums the jail's processes, for a host where the kernel is
// not accounting.
//
//	ps -J <name> -o rss= -o pcpu=
//
// Measured against rctl on three jails: the resident sums land within 1% and
// the process counts are identical, because racct's memoryuse is the sum of
// these same resident sizes (JD-031). `rss` is in kibibytes.
//
// Two separate -o flags, not one comma-separated list: `-o rss=,pcpu=`
// suppresses only the first header and prints ",pcpu=" as the second.
// Measured.
func (a *Adapter) JailProcessUsage(ctx context.Context, name string) (metrics.JailUsage, error) {
	if err := checkJailName(name); err != nil {
		return metrics.JailUsage{}, err
	}
	result, err := a.runHost(ctx, system.Command{
		Name:    "ps",
		Args:    []string{"-J", name, "-o", "rss=", "-o", "pcpu="},
		Timeout: metricsTimeout,
	})
	if err != nil {
		// A jail with no processes and a jail that is not there both exit 1,
		// and only the message tells them apart -- measured: the second says
		// "Invalid jail id". Empty is an answer; gone is worth reporting,
		// because the name came from a listing taken a moment ago and the
		// jail stopping in between is the one thing that explains it.
		if exited(result, err) && !strings.Contains(result.Stderr, "Invalid jail id") {
			return metrics.JailUsage{Source: metrics.SourcePS}, nil
		}
		return metrics.JailUsage{}, err
	}

	usage := metrics.JailUsage{Source: metrics.SourcePS}
	for _, line := range nonEmptyLines(result.Stdout) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		resident, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		percent, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		usage.MemoryUse += resident * 1024
		usage.PCPU += percent
		usage.Processes++
	}
	return usage, nil
}

func (a *Adapter) sysctlInt(ctx context.Context, name string) (int64, error) {
	value, err := a.Sysctl(ctx, name)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %q is not a number", name, value)
	}
	return parsed, nil
}

var (
	_ metrics.HostMetrics     = (*Adapter)(nil)
	_ metrics.JailUsageSource = (*Adapter)(nil)
)
