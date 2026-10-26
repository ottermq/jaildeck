package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
)

// Service assembles a report from the host and every running jail.
//
// It holds exactly one thing: the previous CPU reading. A host CPU
// percentage exists only between two readings -- kern.cp_time is cumulative
// and there is no kernel-maintained utilisation figure -- so either something
// remembers the last look or every request sleeps a second to measure a
// second that has already passed.
//
// It is in memory and not persisted, for the reason JD-008 gives for a
// template's `running`: a restart means the measurement genuinely did not
// happen, and the truthful report is that there is no percentage yet rather
// than a zero.
type Service struct {
	host  HostMetrics
	usage JailUsageSource
	jails JailSource
	disks DiskSource

	mu       sync.Mutex
	previous *reading
}

type reading struct {
	ticks Ticks
	at    time.Time
}

func NewService(host HostMetrics, usage JailUsageSource, jails JailSource, disks DiskSource) *Service {
	return &Service{host: host, usage: usage, jails: jails, disks: disks}
}

// Report reads the host and every running jail.
//
// The host is answered even when the jails cannot be listed, and a jail that
// cannot be read is reported with its reason rather than failing the whole
// request: a dashboard that goes blank because one jail is mid-stop is worse
// than one with a gap in it.
func (s *Service) Report(ctx context.Context) (Report, error) {
	if s.host == nil {
		return Report{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck is running without the host access it needs to measure anything")
	}
	host, err := s.readHost(ctx)
	if err != nil {
		return Report{}, err
	}
	report := Report{Host: host, Jail: make([]JailUsage, 0), Disks: make([]Disk, 0)}

	// Disks are read before the jails and never fail the report. A host
	// without smartmontools has none to read, and a smartctl unhappy about
	// one device should not cost the operator the rest of the page (JD-032).
	if s.disks != nil {
		disks, err := s.disks.Disks(ctx)
		if err != nil {
			common.Logger(ctx, "metrics").Warn("reporting without disk health",
				slog.Any("err", err))
			report.DisksUnavailable = err.Error()
		} else if disks != nil {
			report.Disks = disks
		}
	}

	if s.jails == nil || s.usage == nil {
		return report, nil
	}
	states, err := s.jails.JailStates(ctx)
	if err != nil {
		return report, err
	}
	report.Configured = len(states)

	// Asked once, not once per jail: it cannot change between two jails of
	// the same request, and it decides which reader runs for all of them.
	accounting, err := s.usage.RacctEnabled(ctx)
	if err != nil {
		return report, common.NewAppError(http.StatusInternalServerError,
			"could not read whether this host accounts resource usage: "+err.Error())
	}

	running := make([]string, 0, len(states))
	for name, up := range states {
		// A stopped jail has no usage to read, and rctl would answer for it
		// with zeros that look like a running jail doing nothing.
		if up {
			running = append(running, name)
		}
	}
	sort.Strings(running)

	for _, name := range running {
		report.Jail = append(report.Jail, s.usageOf(ctx, name, accounting))
	}
	return report, nil
}

func (s *Service) usageOf(ctx context.Context, name string, accounting bool) JailUsage {
	read := s.usage.JailProcessUsage
	if accounting {
		read = s.usage.JailUsage
	}
	usage, err := read(ctx, name)
	if err != nil {
		return JailUsage{Jail: name, Error: err.Error()}
	}
	usage.Jail = name
	return usage
}

func (s *Service) readHost(ctx context.Context) (Host, error) {
	load, err := s.host.LoadAverage(ctx)
	if err != nil {
		return Host{}, common.NewAppError(http.StatusInternalServerError,
			"could not read the load average: "+err.Error())
	}
	ticks, cpus, err := s.host.CPUTicks(ctx)
	if err != nil {
		return Host{}, common.NewAppError(http.StatusInternalServerError,
			"could not read the CPU counters: "+err.Error())
	}
	memory, err := s.host.Memory(ctx)
	if err != nil {
		return Host{}, common.NewAppError(http.StatusInternalServerError,
			"could not read memory: "+err.Error())
	}
	pools, err := s.host.PoolUsage(ctx)
	if err != nil {
		return Host{}, common.NewAppError(http.StatusInternalServerError,
			"could not read the pools: "+err.Error())
	}

	host := Host{Load: load, Memory: memory, Pools: pools,
		CPU: CPU{CPUs: cpus, Ticks: ticks}}

	// A rate that cannot be read costs the seconds and nothing else, so it is
	// not worth failing a report over: the ticks are still true, and saying
	// nothing about their unit is better than guessing at it (JD-033).
	if rate, err := s.host.TickRate(ctx); err != nil {
		common.Logger(ctx, "metrics").Warn("reporting ticks without their rate",
			slog.Any("err", err))
	} else {
		host.CPU.TicksPerSecond = rate
	}

	if percent, over, ok := s.rate(ticks); ok {
		host.CPU.Percent, host.CPU.OverSeconds = &percent, &over
	}
	return host, nil
}

// rate turns this reading and the last one into a percentage, and records
// this one as the baseline for the next call.
//
// It reports nothing on the first call after a restart, and nothing when the
// total has not moved forward -- which covers both an interval too short to
// have ticked and a host that rebooted under us, since a counter that went
// backwards means a different uptime and a number derived from two of those
// measures nothing.
//
// Busy needs no guard of its own: idle is a component of total, so busy
// cannot fall while total rises.
func (s *Service) rate(now Ticks) (percent float64, over float64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	taken := time.Now()
	previous := s.previous
	s.previous = &reading{ticks: now, at: taken}

	if previous == nil {
		return 0, 0, false
	}
	total := now.Total()
	wasTotal := previous.ticks.Total()
	if total <= wasTotal {
		return 0, 0, false
	}
	elapsed := taken.Sub(previous.at).Seconds()
	if elapsed <= 0 {
		return 0, 0, false
	}
	busy := float64(now.Busy()-previous.ticks.Busy()) / float64(total-wasTotal) * 100
	return busy, elapsed, true
}
