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

// 127 rather than a round number on purpose: it is isengard's real stathz,
// and anything written against this fake that quietly assumes 100 or 1000
// breaks here rather than on the host (JD-033).
func (h *HostMetrics) TickRate(context.Context) (int, error) { return 127, nil }

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

// Disks is a believable set to look at: a healthy mirror, one half of it
// with a failing attribute, an NVMe, and a disk no pool stands on. The
// failing one is modelled on isengard's ada3, which is the shape the whole
// feature exists for -- its own assessment passes while an attribute does
// not (JD-032).
func (h *HostMetrics) Disks(context.Context) ([]metrics.Disk, error) {
	rpm5400, rpm7200, ssd := 5400, 7200, 0
	temp31, temp27, temp32, temp40 := 31, 27, 32, 40
	hours19k, hours8k, hours115, hours2k := int64(19256), int64(8789), int64(115), int64(2104)
	return []metrics.Disk{
		{
			Device: "ada2", Kind: metrics.DiskATA, Pool: "backup", Vdev: "gpt/backup0",
			Model: "APPLE HDD HTS541010A9E662", Serial: "XXXXXXXXXXX",
			Capacity: 1000204886016, RotationRate: &rpm5400, Passed: true,
			TemperatureC: &temp27, PowerOnHours: &hours8k,
			SelfTest: &metrics.SelfTest{Type: "Short offline",
				Status: "Completed without error", Passed: true, Hours: 8771},
		},
		{
			// The specimen. Passed and failing at the same time.
			Device: "ada3", Kind: metrics.DiskATA, Pool: "backup", Vdev: "gpt/backup1",
			Model: "ST500LM000-1EJ162", Serial: "XXXXXXXX",
			Capacity: 500107862016, RotationRate: &rpm5400, Passed: true,
			TemperatureC: &temp31, PowerOnHours: &hours19k,
			Failing: []metrics.Attribute{{
				ID: 184, Name: "End-to-End_Error", Value: 91, Worst: 91,
				Threshold: 99, Raw: "9",
			}},
			Failed: []metrics.Attribute{{
				ID: 190, Name: "Airflow_Temperature_Cel", Value: 69, Worst: 30,
				Threshold: 45, Raw: "31 (8 71 32 26 0)",
			}},
			SelfTest: &metrics.SelfTest{Type: "Extended offline",
				Status: "Completed without error", Passed: true, Hours: 19149},
		},
		{
			// ZFS and the kernel call it nda0; smartctl will only answer
			// for it as /dev/nvme0, so the path comes along (JD-032).
			Device: "nda0", Kind: metrics.DiskNVMe, Pool: "zroot", Vdev: "nda0p4",
			SmartPath: "/dev/nvme0",
			Model:     "Patriot M.2 P300 256GB", Serial: "XXXXXXXXXXXXXXXXXXXX",
			Capacity: 256060514304, RotationRate: &ssd, Passed: true,
			TemperatureC: &temp32, PowerOnHours: &hours115,
			NVMe: &metrics.NVMeHealth{
				CriticalWarning: 0, PercentageUsed: 0, AvailableSpare: 100,
				SpareThreshold: 10, MediaErrors: 0, ErrorLogEntries: 0,
			},
		},
		{
			// No pool stands on this one, and it is reported anyway: a spare
			// can fail too (JD-032).
			Device: "ada4", Kind: metrics.DiskATA,
			Model: "ST1000LM024 HN-M101MBB", Serial: "XXXXXXXXXXXXXX",
			Capacity: 1000204886016, RotationRate: &rpm7200, Passed: true,
			TemperatureC: &temp40, PowerOnHours: &hours2k,
		},
	}, nil
}

var _ metrics.DiskSource = (*HostMetrics)(nil)
