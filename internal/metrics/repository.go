package metrics

import "context"

// HostMetrics reads the host's own numbers. Every method returns what the
// host said and nothing more: the judgments, the arithmetic and the sampling
// are the service's, so the rules test without a FreeBSD host (JD-010).
type HostMetrics interface {
	// LoadAverage is vm.loadavg. The kernel maintains all three, so this
	// needs no sampling and is the one CPU-ish figure available from a
	// single look.
	LoadAverage(ctx context.Context) (LoadAverage, error)
	// CPUTicks is kern.cp_time, cumulative since boot, with the number of
	// CPUs the counters are aggregated across.
	CPUTicks(ctx context.Context) (Ticks, int, error)
	// TickRate is how many CPUTicks make a second -- kern.clockrate's stathz,
	// which is not its hz. Its own method rather than another return from
	// CPUTicks because it is a property of the kernel's clock rather than of
	// a reading, and it does not change between two of them (JD-033).
	TickRate(ctx context.Context) (int, error)
	// Memory is the page counters times the page size.
	Memory(ctx context.Context) (Memory, error)
	// PoolUsage is `zpool list -Hp`. A host without ZFS has none, which is
	// an answer rather than an error. Named apart from the host domain's own
	// Pools, which answers a different question -- which pools exist --
	// about the same command, and is implemented by the same adapter.
	PoolUsage(ctx context.Context) ([]Pool, error)
}

// JailUsageSource reads one jail's usage, two ways.
//
// Which one is used is decided by the host rather than by configuration:
// rctl where the kernel is accounting, ps where it is not. The caller never
// asks for a name the jail listing did not give it, because `rctl -u` answers
// for a name no jail has with every field zero and exit 0 -- measured -- so
// it cannot tell a stopped jail from a typo (JD-031).
type JailUsageSource interface {
	// RacctEnabled reports kern.racct.enable. Asked once per report rather
	// than once per jail: it cannot change between two jails of the same
	// request, and it decides which of the two readers runs.
	RacctEnabled(ctx context.Context) (bool, error)
	// JailUsage reads `rctl -u jail:<name>`.
	JailUsage(ctx context.Context, name string) (JailUsage, error)
	// JailProcessUsage sums `ps -J <name>`: CPU, resident memory and a
	// process count, and nothing else.
	JailProcessUsage(ctx context.Context, name string) (JailUsage, error)
}

// DiskSource reads the host's physical disks and their health (JD-032).
//
// Separate from HostMetrics because it is the one part of a report that can
// be absent for a reason worth stating: smartmontools is a package, not
// base, and a host without it has no disks to report rather than a failed
// request.
type DiskSource interface {
	// Disks returns every device that answers, with the pool it belongs to
	// where one does. A host without smartctl returns none and no error.
	Disks(ctx context.Context) ([]Disk, error)
}

// JailSource says which jails the host has and which of them are up.
//
// A map rather than a type of this package's: the jails domain implements
// this without importing it, which is the direction JD-010 fixes.
type JailSource interface {
	JailStates(ctx context.Context) (map[string]bool, error)
}
