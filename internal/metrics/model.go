package metrics

// Report is the whole of what GET /api/metrics answers: the host, and every
// jail that is up (JD-031).
//
// One request, because the dashboard is one page. Per-jail usage costs one
// `rctl -u` per jail -- measured, there is no form that answers for every
// subject at once -- which is also why it is not folded into GET /api/jails:
// a listing is what an operator opens to start a jail, and it should not pay
// for the page that watches them.
type Report struct {
	Host Host        `json:"host"`
	Jail []JailUsage `json:"jails"`
	// Configured is how many jails the host has in total, running or not,
	// so the dashboard can say "3 of 5" without a second request.
	Configured int `json:"configured"`
	// Disks is every physical device that answers, pooled or not. Empty on
	// a host without smartmontools, which is a package rather than base
	// (JD-032).
	Disks []Disk `json:"disks"`
}

type Host struct {
	Load   LoadAverage `json:"load"`
	CPU    CPU         `json:"cpu"`
	Memory Memory      `json:"memory"`
	Pools  []Pool      `json:"pools"`
}

// LoadAverage is vm.loadavg, which needs no sampling: the kernel maintains
// all three.
type LoadAverage struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

// Ticks are kern.cp_time, cumulative since boot.
type Ticks struct {
	User      uint64 `json:"user"`
	Nice      uint64 `json:"nice"`
	System    uint64 `json:"system"`
	Interrupt uint64 `json:"interrupt"`
	Idle      uint64 `json:"idle"`
}

func (t Ticks) Total() uint64 {
	return t.User + t.Nice + t.System + t.Interrupt + t.Idle
}

// Busy is everything the CPU did other than wait.
func (t Ticks) Busy() uint64 {
	return t.Total() - t.Idle
}

// CPU carries the raw counters and, when there is a previous reading to
// measure against, the percentage between them.
type CPU struct {
	CPUs  int   `json:"cpus"`
	Ticks Ticks `json:"ticks"`
	// Percent is busy time as a share of the interval since the last
	// reading. Absent on the first call after a restart -- there is nothing
	// to measure against, and zero would be a lie (JD-031).
	Percent *float64 `json:"percent,omitempty"`
	// OverSeconds is how long that interval was, so a reading taken five
	// seconds after the last one and one taken five minutes after are told
	// apart rather than read as the same word.
	OverSeconds *float64 `json:"over_seconds,omitempty"`
}

// Memory is the page counters times the page size, against hw.physmem.
//
// A breakdown rather than one "used" figure: on a ZFS host most of Wired is
// the ARC, which is reclaimable, so a single number would read as pressure
// that is not there.
type Memory struct {
	Physical int64 `json:"physical"`
	Free     int64 `json:"free"`
	Active   int64 `json:"active"`
	Inactive int64 `json:"inactive"`
	Wired    int64 `json:"wired"`
}

// Pool is one ZFS pool as `zpool list -Hp` reports it, in exact bytes like
// everything else in this API (JD-022).
type Pool struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Allocated int64  `json:"allocated"`
	Free      int64  `json:"free"`
	Capacity  int    `json:"capacity"`
	Health    string `json:"health"`
}

// Where a jail's usage was read from. The two are not equally complete, so
// the answer says which one it is rather than leaving a consumer to guess
// why a field is missing (JD-031).
const (
	SourceRctl = "rctl"
	SourcePS   = "ps"
)

// JailUsage is one jail's resource usage.
//
// Measured: a sum of `ps -o rss` lands within 1% of rctl's memoryuse and the
// process counts are identical, because racct's memoryuse *is* the sum of the
// same per-process resident sizes. The fallback reads the same quantity
// another way rather than a different quantity under the same name.
type JailUsage struct {
	Jail string `json:"jail"`
	// Source is "rctl" or "ps".
	Source string `json:"source"`
	// PCPU is percent of one core, so a jail using two whole cores reads
	// 200. From racct, which maintains it as a decaying average, or summed
	// from ps.
	PCPU      float64 `json:"pcpu"`
	MemoryUse int64   `json:"memoryuse"`
	Processes int     `json:"processes"`

	// rctl answers these and ps does not.
	VMemoryUse int64 `json:"vmemoryuse,omitempty"`
	OpenFiles  int64 `json:"openfiles,omitempty"`
	CPUTime    int64 `json:"cputime,omitempty"`

	// Error says this one jail could not be read. The rest of the report is
	// still worth having, so a jail that fails is reported rather than
	// failing the request.
	Error string `json:"error,omitempty"`
}

// Where a disk's health came from, and how much of it there is to read. An
// NVMe has no attribute table at all, so a report that pretended otherwise
// would be inventing data (JD-032).
const (
	DiskATA  = "ata"
	DiskNVMe = "nvme"
)

// Disk is one physical device and what it says about itself.
//
// Reported for every device that answers, whether or not a pool stands on it:
// a spare, a scratch disk or one being wiped can each fail, and an operator
// watching disks wants all of them (JD-032).
type Disk struct {
	// Device is the name the host uses -- ada3, nda0, da0.
	Device string `json:"device"`
	// Kind is "ata" or "nvme", and says which of the two halves below to
	// expect.
	Kind string `json:"kind"`
	// Pool is the pool this disk is part of, empty when none is. The join
	// neither tool does alone: smartctl cannot say which pool a failing
	// disk holds up, and zpool status cannot say the disk is failing.
	Pool string `json:"pool,omitempty"`
	// Vdev is the name the pool knows it by -- gpt/backup1, ada0p4 -- which
	// is what `zpool status` prints and therefore what an operator reading
	// that output is looking for.
	Vdev string `json:"vdev,omitempty"`
	// SmartPath is the device path smartctl had to be given, present only
	// when it is not /dev/<Device>. An NVMe is nda0 to ZFS and to the kernel
	// but can only be read as /dev/nvme0 -- smartctl refuses /dev/nda0 -- so
	// an operator told "nda0 is failing" who reaches for smartctl would
	// otherwise be refused with a message about namespace ids (JD-032).
	SmartPath string `json:"smart_path,omitempty"`

	Model string `json:"model"`
	// Serial is reported because the question after "which disk is failing"
	// is "which one do I unplug", and on a four-disk box that otherwise
	// means powering down to read labels.
	Serial   string `json:"serial"`
	Capacity int64  `json:"capacity"`
	// RotationRate is 0 for an SSD and absent where the drive does not say.
	RotationRate *int `json:"rotation_rate,omitempty"`

	// Passed is the drive's **own** overall assessment. It is not a summary
	// of the attributes below and routinely disagrees with them: isengard's
	// ada3 reports true while attribute 184 has been failing for days
	// (JD-032).
	Passed       bool   `json:"passed"`
	TemperatureC *int   `json:"temperature_c,omitempty"`
	PowerOnHours *int64 `json:"power_on_hours,omitempty"`

	// Failing are the attributes smartctl says are below threshold now, and
	// Failed those it says were at some point. Its judgment, reported as
	// given rather than re-derived from value against threshold.
	Failing []Attribute `json:"failing,omitempty"`
	Failed  []Attribute `json:"failed,omitempty"`

	// NVMe carries what an NVMe says instead of an attribute table.
	NVMe *NVMeHealth `json:"nvme,omitempty"`

	// SelfTest is the most recent self-test the drive logged. Read, never
	// started: a long test costs hours of real I/O and choosing when to pay
	// that is an operator's decision (JD-032).
	SelfTest *SelfTest `json:"self_test,omitempty"`
}

// Attribute is one SMART attribute smartctl flagged.
type Attribute struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Value     int    `json:"value"`
	Worst     int    `json:"worst"`
	Threshold int    `json:"threshold"`
	// Raw is the string smartctl printed, never a number to reason about.
	// Seagate encodes several fields into one integer -- ada3's
	// Raw_Read_Error_Rate reads 200021688 and is not a count of anything.
	Raw string `json:"raw"`
}

// NVMeHealth is the NVMe equivalent of a failing attribute. A non-zero
// CriticalWarning is the drive saying something is wrong.
type NVMeHealth struct {
	CriticalWarning int   `json:"critical_warning"`
	PercentageUsed  int   `json:"percentage_used"`
	AvailableSpare  int   `json:"available_spare"`
	SpareThreshold  int   `json:"spare_threshold"`
	MediaErrors     int64 `json:"media_errors"`
	ErrorLogEntries int64 `json:"error_log_entries"`
}

type SelfTest struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Passed bool   `json:"passed"`
	// Hours is the drive's power-on hour count when the test ran, which is
	// how a self-test log dates itself.
	Hours int64 `json:"hours"`
}
