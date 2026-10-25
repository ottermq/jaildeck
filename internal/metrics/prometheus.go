package metrics

import (
	"bufio"
	"io"
	"sort"
	"strconv"
	"strings"
)

// ExpositionContentType is the Prometheus text exposition format, version
// 0.0.4 -- the one every Prometheus understands. OpenMetrics is deliberately
// not offered: it would be a second format to keep correct for a scraper that
// already reads this one.
const ExpositionContentType = "text/plain; version=0.0.4; charset=utf-8"

// WriteExposition writes one report in Prometheus exposition format.
//
// Hand-written rather than through a client library, because Jail Deck takes
// no dependency it can avoid and this format is twelve lines of writer. The
// cost is that every rule below -- escaping, ordering, one HELP per name --
// is ours to get right, which is what the tests are for.
//
// Units are Prometheus's conventions rather than the JSON API's field names:
// bytes, seconds and ratios, because the audience here writes rate() and
// expects base units (JD-033).
func WriteExposition(w io.Writer, report Report) error {
	out := &exposition{w: newWriter(w)}

	out.host(report.Host)
	out.pools(report.Host.Pools)
	out.jails(report)
	out.disks(report.Disks)

	return out.w.Flush()
}

func (e *exposition) host(host Host) {
	e.gauge("jaildeck_host_load1", "Host load average over one minute.")
	e.value(nil, host.Load.One)
	e.gauge("jaildeck_host_load5", "Host load average over five minutes.")
	e.value(nil, host.Load.Five)
	e.gauge("jaildeck_host_load15", "Host load average over fifteen minutes.")
	e.value(nil, host.Load.Fifteen)

	e.gauge("jaildeck_host_cpus", "Number of CPUs the tick counters are aggregated across.")
	e.value(nil, float64(host.CPU.CPUs))

	// The raw counters, always. They are what the kernel keeps, and a ratio
	// of two of them -- busy over total -- is correct whatever a tick is
	// worth, which is why they are published even when the rate is not known.
	e.counter("jaildeck_host_cpu_ticks_total",
		"Cumulative kern.cp_time counters, in statclock ticks.")
	for _, mode := range cpuModes(host.CPU.Ticks) {
		e.value(labels{{"mode", mode.name}}, float64(mode.ticks))
	}

	// Seconds only where the rate was read. A tick is stathz, not hz, and
	// guessing would be an eightfold error that looks plausible (JD-033).
	if host.CPU.TicksPerSecond > 0 {
		e.counter("jaildeck_host_cpu_seconds_total",
			"Cumulative CPU time by mode, converted with kern.clockrate's stathz.")
		for _, mode := range cpuModes(host.CPU.Ticks) {
			e.value(labels{{"mode", mode.name}},
				float64(mode.ticks)/float64(host.CPU.TicksPerSecond))
		}
	}

	e.gauge("jaildeck_host_memory_bytes", "Host memory by state.")
	for _, state := range []struct {
		name  string
		bytes int64
	}{
		{"physical", host.Memory.Physical},
		{"free", host.Memory.Free},
		{"active", host.Memory.Active},
		{"inactive", host.Memory.Inactive},
		// Most of wired is the ZFS ARC and is reclaimable; it is a state
		// rather than a component of a single "used" figure (JD-031).
		{"wired", host.Memory.Wired},
	} {
		e.value(labels{{"state", state.name}}, float64(state.bytes))
	}
}

type cpuMode struct {
	name  string
	ticks uint64
}

func cpuModes(ticks Ticks) []cpuMode {
	return []cpuMode{
		{"user", ticks.User}, {"nice", ticks.Nice}, {"system", ticks.System},
		{"interrupt", ticks.Interrupt}, {"idle", ticks.Idle},
	}
}

func (e *exposition) pools(pools []Pool) {
	if len(pools) == 0 {
		return
	}
	e.gauge("jaildeck_pool_size_bytes", "Pool size.")
	for _, pool := range pools {
		e.value(labels{{"pool", pool.Name}}, float64(pool.Size))
	}
	e.gauge("jaildeck_pool_allocated_bytes", "Pool space allocated.")
	for _, pool := range pools {
		e.value(labels{{"pool", pool.Name}}, float64(pool.Allocated))
	}
	e.gauge("jaildeck_pool_free_bytes", "Pool space free.")
	for _, pool := range pools {
		e.value(labels{{"pool", pool.Name}}, float64(pool.Free))
	}
	// A ratio rather than zpool's percentage: Prometheus rules read 0-1.
	e.gauge("jaildeck_pool_capacity_ratio", "Pool capacity used, 0 to 1.")
	for _, pool := range pools {
		e.value(labels{{"pool", pool.Name}}, float64(pool.Capacity)/100)
	}
	// Health as a label on a constant 1, not as a number. ONLINE, DEGRADED
	// and FAULTED are not a scale, and giving them one would invite
	// comparisons that mean nothing.
	e.gauge("jaildeck_pool_health_info", "Pool health, as zpool reports it.")
	for _, pool := range pools {
		e.value(labels{{"pool", pool.Name}, {"health", pool.Health}}, 1)
	}
}

func (e *exposition) jails(report Report) {
	e.gauge("jaildeck_jails_configured", "Jails this host has, running or not.")
	e.value(nil, float64(report.Configured))
	e.gauge("jaildeck_jails_running", "Jails that are up.")
	e.value(nil, float64(len(report.Jail)))

	if len(report.Jail) == 0 {
		return
	}

	// Every jail is read the same way within one report, so the source is a
	// property of the report rather than of a jail (JD-031).
	if source := report.Jail[0].Source; source != "" {
		e.gauge("jaildeck_usage_source_info", "Where per-jail usage was read from.")
		e.value(labels{{"source", source}}, 1)
	}

	// A jail that could not be read is published as unreadable rather than
	// left out: a series that disappears reads as a jail that stopped.
	e.gauge("jaildeck_jail_readable", "1 when this jail's usage could be read.")
	for _, jail := range report.Jail {
		e.value(labels{{"jail", jail.Jail}}, boolean(jail.Error == ""))
	}

	readable := make([]JailUsage, 0, len(report.Jail))
	for _, jail := range report.Jail {
		if jail.Error == "" {
			readable = append(readable, jail)
		}
	}
	if len(readable) == 0 {
		return
	}

	// Cores, not pcpu's percent-of-one-core. A jail on two whole cores reads
	// 2 here and 200 in the JSON, and 200 beside anything else is the unit
	// that confuses (JD-031, JD-033).
	e.gauge("jaildeck_jail_cpu_cores", "Cores in use, as a decaying average.")
	for _, jail := range readable {
		e.value(labels{{"jail", jail.Jail}}, jail.PCPU/100)
	}
	e.gauge("jaildeck_jail_memory_bytes", "Resident memory.")
	for _, jail := range readable {
		e.value(labels{{"jail", jail.Jail}}, float64(jail.MemoryUse))
	}
	e.gauge("jaildeck_jail_processes", "Processes in this jail.")
	for _, jail := range readable {
		e.value(labels{{"jail", jail.Jail}}, float64(jail.Processes))
	}

	// The three rctl answers and ps does not. Written only where they were
	// actually read: a zero would be indistinguishable from a jail with no
	// virtual memory, which no jail has (JD-031).
	e.optional("jaildeck_jail_vmemory_bytes", "Virtual memory. Only where rctl is the source.",
		readable, func(jail JailUsage) (float64, bool) {
			return float64(jail.VMemoryUse), jail.VMemoryUse > 0
		})
	e.optional("jaildeck_jail_open_files", "Open file descriptors. Only where rctl is the source.",
		readable, func(jail JailUsage) (float64, bool) {
			return float64(jail.OpenFiles), jail.OpenFiles > 0
		})
	// A genuine cumulative counter, unlike cpu_cores above -- a different
	// quantity, not the same one renamed.
	e.optionalCounter("jaildeck_jail_cpu_seconds_total",
		"Cumulative CPU time. Only where rctl is the source.",
		readable, func(jail JailUsage) (float64, bool) {
			return float64(jail.CPUTime), jail.CPUTime > 0
		})
}

func (e *exposition) disks(disks []Disk) {
	if len(disks) == 0 {
		return
	}
	// Identity as labels on a constant 1. Model and serial change when a disk
	// is replaced, and a metric name must not.
	e.gauge("jaildeck_disk_info", "Disk identity, including the pool it holds up.")
	for _, disk := range disks {
		e.value(labels{
			{"device", disk.Device}, {"kind", disk.Kind},
			{"model", disk.Model}, {"serial", disk.Serial},
			{"pool", disk.Pool}, {"vdev", disk.Vdev},
		}, 1)
	}

	// The drive's OWN verdict, which is not a summary of the attributes
	// below and routinely disagrees with them. Both are published precisely
	// so a rule can catch the disagreement (JD-032).
	e.gauge("jaildeck_disk_passed", "1 when the drive's own overall assessment passes.")
	for _, disk := range disks {
		e.value(labels{{"device", disk.Device}}, boolean(disk.Passed))
	}
	e.gauge("jaildeck_disk_failing_attributes",
		"SMART attributes smartctl says are below threshold now.")
	for _, disk := range disks {
		e.value(labels{{"device", disk.Device}}, float64(len(disk.Failing)))
	}
	e.gauge("jaildeck_disk_failed_attributes",
		"SMART attributes smartctl says were below threshold at some point.")
	for _, disk := range disks {
		e.value(labels{{"device", disk.Device}}, float64(len(disk.Failed)))
	}

	e.gauge("jaildeck_disk_capacity_bytes", "Disk capacity.")
	for _, disk := range disks {
		e.value(labels{{"device", disk.Device}}, float64(disk.Capacity))
	}
	e.optionalDisk("jaildeck_disk_temperature_celsius", "Drive temperature.",
		disks, func(disk Disk) (float64, bool) {
			if disk.TemperatureC == nil {
				return 0, false
			}
			return float64(*disk.TemperatureC), true
		})
	// Seconds, not the drive's hours: the conversion is exact, so nothing is
	// invented by making it the base unit Prometheus expects.
	e.optionalDisk("jaildeck_disk_power_on_seconds", "Time the drive has been powered on.",
		disks, func(disk Disk) (float64, bool) {
			if disk.PowerOnHours == nil {
				return 0, false
			}
			return float64(*disk.PowerOnHours) * 3600, true
		})
	e.optionalDisk("jaildeck_disk_self_test_passed",
		"1 when the most recent self-test in the drive's log passed.",
		disks, func(disk Disk) (float64, bool) {
			if disk.SelfTest == nil {
				return 0, false
			}
			return boolean(disk.SelfTest.Passed), true
		})

	e.attributes(disks)
	e.nvme(disks)
}

// attributes publishes only the attributes smartctl flagged, with its own
// judgment as a label. The whole table would be a hundred series per disk of
// which two matter, and which two is exactly what smartctl already says.
func (e *exposition) attributes(disks []Disk) {
	type flagged struct {
		disk      Disk
		attribute Attribute
		when      string
	}
	rows := make([]flagged, 0)
	for _, disk := range disks {
		for _, attribute := range disk.Failing {
			rows = append(rows, flagged{disk, attribute, "now"})
		}
		for _, attribute := range disk.Failed {
			rows = append(rows, flagged{disk, attribute, "past"})
		}
	}
	if len(rows) == 0 {
		return
	}

	e.gauge("jaildeck_disk_attribute_value",
		"Normalised value of a SMART attribute smartctl flagged.")
	for _, row := range rows {
		e.value(attributeLabels(row.disk, row.attribute, row.when), float64(row.attribute.Value))
	}
	e.gauge("jaildeck_disk_attribute_threshold",
		"Threshold of a SMART attribute smartctl flagged.")
	for _, row := range rows {
		e.value(attributeLabels(row.disk, row.attribute, row.when), float64(row.attribute.Threshold))
	}
	// The raw value is a label and never a number. Vendors pack several
	// fields into that integer -- a Seagate here reads 200021688, which is
	// not a count of anything -- so it is carried as the string smartctl
	// printed, where no rule can do arithmetic on it (JD-032).
	e.gauge("jaildeck_disk_attribute_info",
		"A flagged SMART attribute, with the raw value smartctl printed.")
	for _, row := range rows {
		attributeLabels := attributeLabels(row.disk, row.attribute, row.when)
		e.value(append(attributeLabels, label{"raw", row.attribute.Raw}), 1)
	}
}

func attributeLabels(disk Disk, attribute Attribute, when string) labels {
	return labels{
		{"device", disk.Device},
		{"id", strconv.Itoa(attribute.ID)},
		{"name", attribute.Name},
		{"when_failed", when},
	}
}

// nvme publishes what an NVMe says instead of an attribute table. Only for
// the disks that have one: an ATA drive given these fields would be reporting
// numbers it never produced (JD-032).
func (e *exposition) nvme(disks []Disk) {
	nvmes := make([]Disk, 0)
	for _, disk := range disks {
		if disk.NVMe != nil {
			nvmes = append(nvmes, disk)
		}
	}
	if len(nvmes) == 0 {
		return
	}

	e.gauge("jaildeck_disk_nvme_critical_warning",
		"NVMe critical warning bits. Non-zero is the drive saying something is wrong.")
	for _, disk := range nvmes {
		e.value(labels{{"device", disk.Device}}, float64(disk.NVMe.CriticalWarning))
	}
	e.gauge("jaildeck_disk_nvme_used_ratio", "NVMe endurance consumed, 0 to 1.")
	for _, disk := range nvmes {
		e.value(labels{{"device", disk.Device}}, float64(disk.NVMe.PercentageUsed)/100)
	}
	e.gauge("jaildeck_disk_nvme_available_spare_ratio", "NVMe spare blocks remaining, 0 to 1.")
	for _, disk := range nvmes {
		e.value(labels{{"device", disk.Device}}, float64(disk.NVMe.AvailableSpare)/100)
	}
	e.gauge("jaildeck_disk_nvme_spare_threshold_ratio",
		"The spare level below which the drive considers itself in trouble, 0 to 1.")
	for _, disk := range nvmes {
		e.value(labels{{"device", disk.Device}}, float64(disk.NVMe.SpareThreshold)/100)
	}
	e.counter("jaildeck_disk_nvme_media_errors_total", "NVMe media and data integrity errors.")
	for _, disk := range nvmes {
		e.value(labels{{"device", disk.Device}}, float64(disk.NVMe.MediaErrors))
	}
	e.counter("jaildeck_disk_nvme_error_log_entries_total", "Entries in the NVMe error log.")
	for _, disk := range nvmes {
		e.value(labels{{"device", disk.Device}}, float64(disk.NVMe.ErrorLogEntries))
	}
}

// --- the writer --------------------------------------------------------

func newWriter(w io.Writer) *bufio.Writer { return bufio.NewWriter(w) }

type label struct{ name, value string }

type labels []label

// exposition writes the format and nothing else. It keeps the current metric
// name so a value line cannot be written under the wrong one, and refuses to
// repeat a HELP, because two blocks for one name is the error a scraper
// rejects the whole body for.
type exposition struct {
	w       *bufio.Writer
	current string
	written map[string]bool
}

func (e *exposition) header(name, kind, help string) {
	if e.written == nil {
		e.written = map[string]bool{}
	}
	if e.written[name] {
		// A programming error here would silently corrupt the body, so it is
		// made inert rather than written out twice.
		e.current = name
		return
	}
	e.written[name] = true
	e.current = name

	e.w.WriteString("# HELP ")
	e.w.WriteString(name)
	e.w.WriteString(" ")
	e.w.WriteString(escapeHelp(help))
	e.w.WriteString("\n# TYPE ")
	e.w.WriteString(name)
	e.w.WriteString(" ")
	e.w.WriteString(kind)
	e.w.WriteString("\n")
}

func (e *exposition) gauge(name, help string)   { e.header(name, "gauge", help) }
func (e *exposition) counter(name, help string) { e.header(name, "counter", help) }

func (e *exposition) value(with labels, value float64) {
	e.w.WriteString(e.current)
	if len(with) > 0 {
		e.w.WriteString("{")
		for i, l := range with {
			if i > 0 {
				e.w.WriteString(",")
			}
			e.w.WriteString(l.name)
			e.w.WriteString(`="`)
			e.w.WriteString(escapeLabel(l.value))
			e.w.WriteString(`"`)
		}
		e.w.WriteString("}")
	}
	e.w.WriteString(" ")
	e.w.WriteString(formatValue(value))
	e.w.WriteString("\n")
}

// optional writes a jail metric only for the jails that have it, and writes
// no block at all when none do -- a HELP with no samples under it is noise a
// scraper carries forever.
func (e *exposition) optional(name, help string, jails []JailUsage,
	read func(JailUsage) (float64, bool)) {
	e.optionalJail(name, "gauge", help, jails, read)
}

func (e *exposition) optionalCounter(name, help string, jails []JailUsage,
	read func(JailUsage) (float64, bool)) {
	e.optionalJail(name, "counter", help, jails, read)
}

func (e *exposition) optionalJail(name, kind, help string, jails []JailUsage,
	read func(JailUsage) (float64, bool)) {
	type sample struct {
		jail  string
		value float64
	}
	samples := make([]sample, 0, len(jails))
	for _, jail := range jails {
		if value, ok := read(jail); ok {
			samples = append(samples, sample{jail.Jail, value})
		}
	}
	if len(samples) == 0 {
		return
	}
	e.header(name, kind, help)
	for _, s := range samples {
		e.value(labels{{"jail", s.jail}}, s.value)
	}
}

func (e *exposition) optionalDisk(name, help string, disks []Disk,
	read func(Disk) (float64, bool)) {
	type sample struct {
		device string
		value  float64
	}
	samples := make([]sample, 0, len(disks))
	for _, disk := range disks {
		if value, ok := read(disk); ok {
			samples = append(samples, sample{disk.Device, value})
		}
	}
	if len(samples) == 0 {
		return
	}
	e.gauge(name, help)
	for _, s := range samples {
		e.value(labels{{"device", s.device}}, s.value)
	}
}

func boolean(yes bool) float64 {
	if yes {
		return 1
	}
	return 0
}

// formatValue writes the shortest representation that round-trips. 'g' keeps
// whole numbers whole -- 1 rather than 1.000000 -- and never produces an
// exponent a scraper cannot read, because Prometheus accepts Go's form.
func formatValue(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// Label values are quoted, so a backslash, a quote or a newline inside one
// ends the value early and corrupts every sample after it. A disk model is
// free text from the drive's own firmware, which is reason enough.
var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// HELP is not quoted. Only a backslash and a newline can end it.
var helpEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`)

func escapeLabel(value string) string { return labelEscaper.Replace(value) }
func escapeHelp(help string) string   { return helpEscaper.Replace(help) }

// sortedNames is used by the tests to assert the body carries no duplicate
// metric name; keeping it here keeps the rule beside the writer that enforces
// it.
func (e *exposition) sortedNames() []string {
	names := make([]string, 0, len(e.written))
	for name := range e.written {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
