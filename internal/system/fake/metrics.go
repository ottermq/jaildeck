package fake

import (
	"context"
	"math/rand/v2"

	"github.com/ottermq/jaildeck/internal/metrics"
)

// HostMetrics is an in-memory host to watch. The counters move between calls
// -- a dashboard whose numbers never change cannot be told from one whose
// polling has stopped, which is the bug most worth being able to see here.
type HostMetrics struct {
	ticks metrics.Ticks
}

func NewHostMetrics() *HostMetrics {
	return &HostMetrics{ticks: metrics.Ticks{
		User: 226572, Nice: 155, System: 847113, Interrupt: 47482, Idle: 677314310,
	}}
}

func (h *HostMetrics) LoadAverage(context.Context) (metrics.LoadAverage, error) {
	return metrics.LoadAverage{One: 0.42, Five: 0.31, Fifteen: 0.18}, nil
}

// CPUTicks advances by roughly a second of a sixteen-core host at a tenth
// busy, so two calls apart produce a believable percentage.
func (h *HostMetrics) CPUTicks(context.Context) (metrics.Ticks, int, error) {
	busy := uint64(180 + rand.IntN(60))
	h.ticks.User += busy
	h.ticks.System += busy / 4
	h.ticks.Idle += 2048 - busy
	return h.ticks, 16, nil
}

func (h *HostMetrics) Memory(context.Context) (metrics.Memory, error) {
	const page = 4096
	return metrics.Memory{
		Physical: 17120813056,
		Free:     3111566 * page,
		Active:   10270 * page,
		Inactive: 106011 * page,
		Wired:    845271 * page,
	}, nil
}

func (h *HostMetrics) PoolUsage(context.Context) ([]metrics.Pool, error) {
	return []metrics.Pool{
		{Name: "zroot", Size: 253403070464, Allocated: 2878607360,
			Free: 250524463104, Capacity: 1, Health: "ONLINE"},
		{Name: "backup", Size: 498216206336, Allocated: 9126301696,
			Free: 489089904640, Capacity: 1, Health: "DEGRADED"},
	}, nil
}

var _ metrics.HostMetrics = (*HostMetrics)(nil)

// RacctEnabled is already on this type for the limits work, and says the
// fake host accounts -- so the dev server exercises the rctl path and the ps
// fallback is left to its own tests.

// JailUsage invents a steady figure per jail rather than a random one: a
// number that jumps every poll makes a layout look wrong when it is not.
func (s *JailSystem) JailUsage(_ context.Context, name string) (metrics.JailUsage, error) {
	seed := 0
	for _, r := range name {
		seed += int(r)
	}
	return metrics.JailUsage{
		Source:     metrics.SourceRctl,
		PCPU:       float64(seed % 180),
		MemoryUse:  int64(seed%400+20) << 20,
		Processes:  seed%17 + 1,
		VMemoryUse: int64(seed%900+200) << 20,
		OpenFiles:  int64(seed%4000 + 100),
		CPUTime:    int64(seed % 9000),
	}, nil
}

func (s *JailSystem) JailProcessUsage(ctx context.Context, name string) (metrics.JailUsage, error) {
	usage, err := s.JailUsage(ctx, name)
	if err != nil {
		return metrics.JailUsage{}, err
	}
	// What ps cannot answer, absent rather than carried over.
	return metrics.JailUsage{
		Source: metrics.SourcePS, PCPU: usage.PCPU,
		MemoryUse: usage.MemoryUse, Processes: usage.Processes,
	}, nil
}

var _ metrics.JailUsageSource = (*JailSystem)(nil)
