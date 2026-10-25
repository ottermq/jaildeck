package metrics

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// render writes one report and returns the body.
func render(t *testing.T, report Report) string {
	t.Helper()
	var body bytes.Buffer
	if err := WriteExposition(&body, report); err != nil {
		t.Fatalf("WriteExposition: %v", err)
	}
	return body.String()
}

// aReport is a host with everything on it: two pools, two jails read with
// rctl, and three disks of which one is failing an attribute and one is an
// NVMe. The numbers are isengard's.
func aReport() Report {
	percent, over := 13.9, 5.0
	temp31, temp35 := 31, 35
	hours19k, hours116 := int64(19257), int64(116)
	rpm, ssd := 5400, 0
	return Report{
		Host: Host{
			Load: LoadAverage{One: 0.42, Five: 0.31, Fifteen: 0.18},
			CPU: CPU{
				CPUs:  16,
				Ticks: Ticks{User: 266024, Nice: 155, System: 990710, Interrupt: 55092, Idle: 782769682},
				// stathz on isengard, not hz.
				TicksPerSecond: 127,
				Percent:        &percent,
				OverSeconds:    &over,
			},
			Memory: Memory{Physical: 17120813056, Free: 12745015296,
				Active: 41943040, Inactive: 434110464, Wired: 3435973836},
			Pools: []Pool{
				{Name: "backup", Size: 498216206336, Allocated: 9126805504,
					Free: 489089400832, Capacity: 1, Health: "DEGRADED"},
				{Name: "tank", Size: 996432412672, Allocated: 2899102924,
					Free: 993533309748, Capacity: 1, Health: "ONLINE"},
			},
		},
		Configured: 3,
		Jail: []JailUsage{
			{Jail: "ottermq", Source: SourceRctl, PCPU: 175, MemoryUse: 20684800,
				Processes: 6, VMemoryUse: 1865551872, OpenFiles: 648, CPUTime: 226},
			{Jail: "postgres", Source: SourceRctl, PCPU: 12, MemoryUse: 290263040,
				Processes: 13, VMemoryUse: 1865551872, OpenFiles: 9256, CPUTime: 901},
		},
		Disks: []Disk{
			{Device: "ada0", Kind: DiskATA, Pool: "tank", Vdev: "ada0p4",
				Model: "ST1000LM024 HN-M101MBB", Serial: "S03XXXXXXXXXXX",
				Capacity: 1000204886016, RotationRate: &rpm, Passed: true,
				TemperatureC: &temp31, PowerOnHours: &hours19k,
				SelfTest: &SelfTest{Type: "Short offline",
					Status: "Completed without error", Passed: true, Hours: 19257}},
			{Device: "ada3", Kind: DiskATA, Pool: "backup", Vdev: "gpt/backup1",
				Model: "ST500LM000-1EJ162", Serial: "S06XXXXX",
				Capacity: 500107862016, RotationRate: &rpm, Passed: true,
				TemperatureC: &temp31, PowerOnHours: &hours19k,
				Failing: []Attribute{{ID: 184, Name: "End-to-End_Error",
					Value: 91, Worst: 91, Threshold: 99, Raw: "9"}},
				Failed: []Attribute{{ID: 190, Name: "Airflow_Temperature_Cel",
					Value: 70, Worst: 30, Threshold: 45, Raw: "30 (8 71 32 26 0)"}}},
			{Device: "nda0", Kind: DiskNVMe, Pool: "zroot", Vdev: "nda0p4",
				SmartPath: "/dev/nvme0", Model: "Patriot M.2 P300 256GB",
				Serial: "S01XXXXXXXXXXXXXXXXX", Capacity: 256060514304,
				RotationRate: &ssd, Passed: true, TemperatureC: &temp35,
				PowerOnHours: &hours116,
				NVMe: &NVMeHealth{CriticalWarning: 0, PercentageUsed: 0,
					AvailableSpare: 100, SpareThreshold: 1}},
		},
	}
}

func hasLine(body, want string) bool {
	for _, line := range strings.Split(body, "\n") {
		if line == want {
			return true
		}
	}
	return false
}

func requireLine(t *testing.T, body, want string) {
	t.Helper()
	if !hasLine(body, want) {
		t.Errorf("missing line:\n  %s", want)
	}
}

func refuseLine(t *testing.T, body, unwanted string) {
	t.Helper()
	if hasLine(body, unwanted) {
		t.Errorf("unexpected line:\n  %s", unwanted)
	}
}

// --- the format itself -------------------------------------------------

var (
	helpRE   = regexp.MustCompile(`^# HELP ([a-zA-Z_:][a-zA-Z0-9_:]*) (.*)$`)
	typeRE   = regexp.MustCompile(`^# TYPE ([a-zA-Z_:][a-zA-Z0-9_:]*) (counter|gauge|histogram|summary|untyped)$`)
	sampleRE = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{.*\})? (\S+)$`)
	labelRE  = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"$`)
)

// checkFormat holds the body to the exposition grammar: every sample under a
// TYPE, every name declared once, every value a number, every label quoted
// and escaped.
//
// Written here because a format nobody validates is a format that drifts, and
// promtool could not be had: `go install .../cmd/promtool@latest` is refused
// because Prometheus's go.mod carries replace directives, and the alternatives
// are a release binary to download or prometheus/common/expfmt as a test
// dependency, which this project does not take.
//
// So this is a parser of our own, and that is weaker evidence than Prometheus
// reading the body. It holds the grammar; it cannot say the scraper is happy.
// The one check that settles it is still a real scrape.
func checkFormat(t *testing.T, body string) {
	t.Helper()
	declared := map[string]string{}
	helped := map[string]bool{}
	for i, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		where := "line " + strconv.Itoa(i+1) + ": " + line
		switch {
		case line == "":
			t.Errorf("%s: blank line", where)
		case strings.HasPrefix(line, "# HELP "):
			match := helpRE.FindStringSubmatch(line)
			if match == nil {
				t.Errorf("%s: malformed HELP", where)
				continue
			}
			if helped[match[1]] {
				t.Errorf("%s: a second HELP for %s", where, match[1])
			}
			helped[match[1]] = true
		case strings.HasPrefix(line, "# TYPE "):
			match := typeRE.FindStringSubmatch(line)
			if match == nil {
				t.Errorf("%s: malformed TYPE", where)
				continue
			}
			if _, already := declared[match[1]]; already {
				t.Errorf("%s: a second TYPE for %s", where, match[1])
			}
			declared[match[1]] = match[2]
		case strings.HasPrefix(line, "#"):
			t.Errorf("%s: a comment that is neither HELP nor TYPE", where)
		default:
			match := sampleRE.FindStringSubmatch(line)
			if match == nil {
				t.Errorf("%s: malformed sample", where)
				continue
			}
			name, labelSet, value := match[1], match[2], match[3]
			if _, ok := declared[name]; !ok {
				t.Errorf("%s: sample for %s before its TYPE", where, name)
			}
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				t.Errorf("%s: %q is not a number", where, value)
			}
			if labelSet == "" {
				continue
			}
			seen := map[string]bool{}
			for _, pair := range splitLabels(labelSet) {
				m := labelRE.FindStringSubmatch(pair)
				if m == nil {
					t.Errorf("%s: malformed label %q", where, pair)
					continue
				}
				if seen[m[1]] {
					t.Errorf("%s: label %s given twice", where, m[1])
				}
				seen[m[1]] = true
			}
		}
	}
	// A HELP with no TYPE, or the other way round, is a block half written.
	for name := range helped {
		if _, ok := declared[name]; !ok {
			t.Errorf("%s has a HELP and no TYPE", name)
		}
	}
	for name := range declared {
		if !helped[name] {
			t.Errorf("%s has a TYPE and no HELP", name)
		}
	}
}

// splitLabels splits `{a="1",b="2"}` on the commas that separate labels,
// never on one inside a quoted value -- "30 (8 71 32 26 0)" has none, but a
// drive model is free text from firmware.
func splitLabels(labelSet string) []string {
	inner := strings.TrimSuffix(strings.TrimPrefix(labelSet, "{"), "}")
	pairs := make([]string, 0)
	quoted, escaped, start := false, false, 0
	for i, r := range inner {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			pairs = append(pairs, inner[start:i])
			start = i + 1
		}
	}
	return append(pairs, inner[start:])
}

func TestExpositionIsWellFormed(t *testing.T) {
	checkFormat(t, render(t, aReport()))
}

func TestExpositionOfAnEmptyHostIsStillWellFormed(t *testing.T) {
	body := render(t, Report{Jail: []JailUsage{}, Disks: []Disk{}})
	checkFormat(t, body)
	// No pools, jails or disks means no blocks for them -- a HELP with no
	// samples under it is noise a scraper carries forever.
	for _, name := range []string{"jaildeck_pool_size_bytes", "jaildeck_disk_info",
		"jaildeck_jail_cpu_cores", "jaildeck_usage_source_info"} {
		if strings.Contains(body, name) {
			t.Errorf("%s was written for a host that has none", name)
		}
	}
	// The host's own figures are always there, so a scrape of a bare host is
	// still a scrape rather than an empty body.
	requireLine(t, body, "jaildeck_jails_configured 0")
	requireLine(t, body, "jaildeck_jails_running 0")
}

// --- the numbers -------------------------------------------------------

func TestHostMetrics(t *testing.T) {
	body := render(t, aReport())
	requireLine(t, body, "jaildeck_host_load1 0.42")
	requireLine(t, body, "jaildeck_host_load5 0.31")
	requireLine(t, body, "jaildeck_host_load15 0.18")
	requireLine(t, body, "jaildeck_host_cpus 16")
	requireLine(t, body, `jaildeck_host_cpu_ticks_total{mode="user"} 266024`)
	requireLine(t, body, `jaildeck_host_cpu_ticks_total{mode="idle"} 7.82769682e+08`)
	requireLine(t, body, `jaildeck_host_memory_bytes{state="physical"} 1.7120813056e+10`)
	requireLine(t, body, `jaildeck_host_memory_bytes{state="wired"} 3.435973836e+09`)
}

// The conversion that is worth a test of its own: kern.cp_time is in stathz,
// which on isengard is 127 while hz is 1000. Dividing by the wrong one is an
// eightfold error that looks entirely plausible (JD-033).
func TestCPUSecondsUseStathzAndNotHz(t *testing.T) {
	body := render(t, aReport())

	// 266024 ticks / 127 = 2094.68..., not 266.024 as hz would give.
	want := strconv.FormatFloat(266024.0/127.0, 'g', -1, 64)
	requireLine(t, body, `jaildeck_host_cpu_seconds_total{mode="user"} `+want)

	byHz := strconv.FormatFloat(266024.0/1000.0, 'g', -1, 64)
	refuseLine(t, body, `jaildeck_host_cpu_seconds_total{mode="user"} `+byHz)
}

// Where the rate could not be read, the seconds are absent rather than wrong.
// The raw counters stay, because a ratio of two of them is correct whatever a
// tick is worth.
func TestCPUSecondsAreAbsentWithoutATickRate(t *testing.T) {
	report := aReport()
	report.Host.CPU.TicksPerSecond = 0
	body := render(t, report)
	checkFormat(t, body)

	if strings.Contains(body, "jaildeck_host_cpu_seconds_total") {
		t.Error("CPU seconds were written without a rate to convert them with")
	}
	requireLine(t, body, `jaildeck_host_cpu_ticks_total{mode="user"} 266024`)
}

func TestPoolMetrics(t *testing.T) {
	body := render(t, aReport())
	requireLine(t, body, `jaildeck_pool_size_bytes{pool="tank"} 9.96432412672e+11`)
	// A ratio, not zpool's percentage.
	requireLine(t, body, `jaildeck_pool_capacity_ratio{pool="tank"} 0.01`)
	// Health as a label on a constant, because ONLINE and DEGRADED are not a
	// scale.
	requireLine(t, body, `jaildeck_pool_health_info{pool="backup",health="DEGRADED"} 1`)
	requireLine(t, body, `jaildeck_pool_health_info{pool="tank",health="ONLINE"} 1`)
}

// 175% of one core is 1.75 cores. The percentage is the unit that confuses
// beside anything else, which the dashboard already found (JD-031).
func TestJailCPUIsInCores(t *testing.T) {
	body := render(t, aReport())
	requireLine(t, body, `jaildeck_jail_cpu_cores{jail="ottermq"} 1.75`)
	requireLine(t, body, `jaildeck_jail_cpu_cores{jail="postgres"} 0.12`)
	// A second name for the same number would be the mistake.
	if strings.Contains(body, "jaildeck_jail_pcpu") {
		t.Error("pcpu is published under a second name as well as cores")
	}
}

func TestJailMetrics(t *testing.T) {
	body := render(t, aReport())
	requireLine(t, body, "jaildeck_jails_configured 3")
	requireLine(t, body, "jaildeck_jails_running 2")
	requireLine(t, body, `jaildeck_usage_source_info{source="rctl"} 1`)
	requireLine(t, body, `jaildeck_jail_memory_bytes{jail="postgres"} 2.9026304e+08`)
	requireLine(t, body, `jaildeck_jail_processes{jail="postgres"} 13`)
	requireLine(t, body, `jaildeck_jail_open_files{jail="postgres"} 9256`)
	requireLine(t, body, `jaildeck_jail_cpu_seconds_total{jail="postgres"} 901`)
	requireLine(t, body, `jaildeck_jail_readable{jail="postgres"} 1`)
}

// ps answers three of these and rctl answers six. The three it cannot are
// absent rather than zero: a zero vmemoryuse is a jail with no virtual
// memory, which no jail has (JD-031).
func TestTheRctlOnlyMetricsAreAbsentUnderPS(t *testing.T) {
	report := aReport()
	report.Jail = []JailUsage{
		{Jail: "ottermq", Source: SourcePS, PCPU: 1, MemoryUse: 20979712, Processes: 6},
	}
	body := render(t, report)
	checkFormat(t, body)

	requireLine(t, body, `jaildeck_usage_source_info{source="ps"} 1`)
	requireLine(t, body, `jaildeck_jail_memory_bytes{jail="ottermq"} 2.0979712e+07`)
	for _, absent := range []string{
		"jaildeck_jail_vmemory_bytes", "jaildeck_jail_open_files",
		"jaildeck_jail_cpu_seconds_total",
	} {
		if strings.Contains(body, absent) {
			t.Errorf("%s was written for a report read with ps", absent)
		}
	}
}

// A jail that could not be read is published as unreadable rather than left
// out. A series that simply disappears reads as a jail that stopped.
func TestAJailThatCouldNotBeReadIsPublishedAsUnreadable(t *testing.T) {
	report := aReport()
	report.Jail = append(report.Jail,
		JailUsage{Jail: "goodiesdb", Error: "rctl: jail not found"})
	body := render(t, report)
	checkFormat(t, body)

	requireLine(t, body, `jaildeck_jail_readable{jail="goodiesdb"} 0`)
	requireLine(t, body, `jaildeck_jail_readable{jail="postgres"} 1`)
	// Its zeros are not published as though they were measurements.
	refuseLine(t, body, `jaildeck_jail_memory_bytes{jail="goodiesdb"} 0`)
	refuseLine(t, body, `jaildeck_jail_cpu_cores{jail="goodiesdb"} 0`)
}

// --- disks -------------------------------------------------------------

func TestDiskMetrics(t *testing.T) {
	body := render(t, aReport())
	requireLine(t, body,
		`jaildeck_disk_info{device="ada3",kind="ata",model="ST500LM000-1EJ162",`+
			`serial="S06XXXXX",pool="backup",vdev="gpt/backup1"} 1`)
	requireLine(t, body, `jaildeck_disk_passed{device="ada3"} 1`)
	requireLine(t, body, `jaildeck_disk_failing_attributes{device="ada3"} 1`)
	requireLine(t, body, `jaildeck_disk_failed_attributes{device="ada3"} 1`)
	requireLine(t, body, `jaildeck_disk_failing_attributes{device="ada0"} 0`)
	// Seconds, not the drive's hours. 19257 × 3600.
	requireLine(t, body, `jaildeck_disk_power_on_seconds{device="ada3"} 6.93252e+07`)
	requireLine(t, body, `jaildeck_disk_temperature_celsius{device="nda0"} 35`)
	requireLine(t, body, `jaildeck_disk_self_test_passed{device="ada0"} 1`)
}

// The pair a rule has to be able to catch: a drive reporting PASSED while an
// attribute is below threshold now. Both are published for exactly that
// reason (JD-032).
func TestAPassingDriveWithAFailingAttributeIsVisibleAsBoth(t *testing.T) {
	body := render(t, aReport())
	requireLine(t, body, `jaildeck_disk_passed{device="ada3"} 1`)
	requireLine(t, body, `jaildeck_disk_failing_attributes{device="ada3"} 1`)
	requireLine(t, body,
		`jaildeck_disk_attribute_value{device="ada3",id="184",`+
			`name="End-to-End_Error",when_failed="now"} 91`)
	requireLine(t, body,
		`jaildeck_disk_attribute_threshold{device="ada3",id="184",`+
			`name="End-to-End_Error",when_failed="now"} 99`)
}

// The raw value is a label and never a number. Attribute 190 here prints
// "30 (8 71 32 26 0)", which is not a quantity at all.
func TestARawAttributeValueIsALabelAndNeverANumber(t *testing.T) {
	body := render(t, aReport())
	requireLine(t, body,
		`jaildeck_disk_attribute_info{device="ada3",id="190",`+
			`name="Airflow_Temperature_Cel",when_failed="past",raw="30 (8 71 32 26 0)"} 1`)
	if strings.Contains(body, "jaildeck_disk_attribute_raw ") {
		t.Error("a raw SMART value was published as a number")
	}
}

// Only the attributes smartctl flagged. The whole table is a hundred series
// per disk of which two matter, and which two is what smartctl already says.
func TestOnlyFlaggedAttributesArePublished(t *testing.T) {
	body := render(t, aReport())
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "jaildeck_disk_attribute_value{device=\"ada0\"") {
			t.Errorf("ada0 flags nothing, yet published %s", line)
		}
	}
}

func TestNVMeMetricsAreOnlyForNVMeDisks(t *testing.T) {
	body := render(t, aReport())
	requireLine(t, body, `jaildeck_disk_nvme_critical_warning{device="nda0"} 0`)
	requireLine(t, body, `jaildeck_disk_nvme_available_spare_ratio{device="nda0"} 1`)
	requireLine(t, body, `jaildeck_disk_nvme_spare_threshold_ratio{device="nda0"} 0.01`)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "jaildeck_disk_nvme") && strings.Contains(line, `device="ada`) {
			t.Errorf("an ATA disk was given NVMe fields: %s", line)
		}
	}
}

// A host with no smartmontools reports no disks, and that must not leave a
// block of HELP lines with nothing under them.
func TestNoDisksMeansNoDiskBlocks(t *testing.T) {
	report := aReport()
	report.Disks = []Disk{}
	body := render(t, report)
	checkFormat(t, body)
	if strings.Contains(body, "jaildeck_disk") {
		t.Error("disk metrics were written for a host that reported none")
	}
}

// --- escaping ----------------------------------------------------------

// A disk model is free text from the drive's own firmware, and a label value
// is quoted. A backslash or a quote inside one ends the value early and
// corrupts every sample after it.
func TestLabelValuesAreEscaped(t *testing.T) {
	report := aReport()
	report.Disks = []Disk{{
		Device: "ada9", Kind: DiskATA, Passed: true,
		Model:  `Weird "Drive" \ Co`,
		Serial: "LINE\nBREAK",
	}}
	body := render(t, report)
	checkFormat(t, body)

	requireLine(t, body,
		`jaildeck_disk_info{device="ada9",kind="ata",model="Weird \"Drive\" \\ Co",`+
			`serial="LINE\nBREAK",pool="",vdev=""} 1`)
}

func TestHelpTextIsEscaped(t *testing.T) {
	var body bytes.Buffer
	out := &exposition{w: newWriter(&body)}
	out.gauge("jaildeck_test", "a help with a \\ and a\nbreak")
	out.value(nil, 1)
	if err := out.w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	requireLine(t, body.String(), `# HELP jaildeck_test a help with a \\ and a\nbreak`)
}

// Two blocks for one metric name is the error a scraper rejects the whole
// body for, so the writer makes a repeat inert rather than writing it twice.
func TestASecondBlockForOneNameIsRefused(t *testing.T) {
	var body bytes.Buffer
	out := &exposition{w: newWriter(&body)}
	out.gauge("jaildeck_test", "first")
	out.value(nil, 1)
	out.gauge("jaildeck_test", "second")
	out.value(nil, 2)
	if err := out.w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	checkFormat(t, body.String())
	if strings.Contains(body.String(), "second") {
		t.Error("a second HELP was written for a name that already had one")
	}
	// The samples still land, under the one block.
	requireLine(t, body.String(), "jaildeck_test 2")
	if names := out.sortedNames(); len(names) != 1 || names[0] != "jaildeck_test" {
		t.Errorf("names = %v, want one", names)
	}
}
