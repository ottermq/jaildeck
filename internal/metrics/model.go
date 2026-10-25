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
