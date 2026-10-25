package metrics

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

// --- doubles -----------------------------------------------------------

type stubHost struct {
	load   LoadAverage
	ticks  Ticks
	cpus   int
	memory Memory
	pools  []Pool

	loadErr  error
	ticksErr error
	poolErr  error
	reads    int
}

func (s *stubHost) LoadAverage(context.Context) (LoadAverage, error) {
	return s.load, s.loadErr
}

func (s *stubHost) CPUTicks(context.Context) (Ticks, int, error) {
	s.reads++
	return s.ticks, s.cpus, s.ticksErr
}

func (s *stubHost) Memory(context.Context) (Memory, error)    { return s.memory, nil }
func (s *stubHost) PoolUsage(context.Context) ([]Pool, error) { return s.pools, s.poolErr }

type stubUsage struct {
	accounting    bool
	accountingErr error
	rctl          map[string]JailUsage
	ps            map[string]JailUsage
	err           error
	asked         []string
	viaPS         []string
}

func (s *stubUsage) RacctEnabled(context.Context) (bool, error) {
	return s.accounting, s.accountingErr
}

func (s *stubUsage) JailUsage(_ context.Context, name string) (JailUsage, error) {
	s.asked = append(s.asked, name)
	if s.err != nil {
		return JailUsage{}, s.err
	}
	return s.rctl[name], nil
}

func (s *stubUsage) JailProcessUsage(_ context.Context, name string) (JailUsage, error) {
	s.asked = append(s.asked, name)
	s.viaPS = append(s.viaPS, name)
	if s.err != nil {
		return JailUsage{}, s.err
	}
	return s.ps[name], nil
}

type stubJails struct {
	states map[string]bool
	err    error
}

func (s *stubJails) JailStates(context.Context) (map[string]bool, error) {
	return s.states, s.err
}

var (
	_ HostMetrics     = (*stubHost)(nil)
	_ JailUsageSource = (*stubUsage)(nil)
	_ JailSource      = (*stubJails)(nil)
)

func aHost() *stubHost {
	return &stubHost{
		load:   LoadAverage{One: 0.5, Five: 0.4, Fifteen: 0.3},
		ticks:  Ticks{User: 100, System: 50, Idle: 850},
		cpus:   16,
		memory: Memory{Physical: 1 << 34, Free: 1 << 33},
		pools:  []Pool{{Name: "tank", Health: "ONLINE"}},
	}
}

func aFleet() *stubJails {
	return &stubJails{states: map[string]bool{
		"postgres": true, "ottermq": true, "stopped-one": false,
	}}
}

func accounting() *stubUsage {
	return &stubUsage{
		accounting: true,
		rctl: map[string]JailUsage{
			"postgres": {Source: SourceRctl, PCPU: 12, MemoryUse: 290263040, Processes: 13,
				VMemoryUse: 1865551872, OpenFiles: 9256, CPUTime: 226},
			"ottermq": {Source: SourceRctl, PCPU: 1, MemoryUse: 20684800, Processes: 6},
		},
		ps: map[string]JailUsage{
			"postgres": {Source: SourcePS, PCPU: 12, MemoryUse: 290476032, Processes: 13},
			"ottermq":  {Source: SourcePS, PCPU: 1, MemoryUse: 20979712, Processes: 6},
		},
	}
}

func appCode(t *testing.T, err error) int {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("want a *common.AppError, got %T: %v", err, err)
	}
	return appErr.Code
}

// --- the report --------------------------------------------------------

func TestReportCoversTheHostAndEveryRunningJail(t *testing.T) {
	host, usage, jails := aHost(), accounting(), aFleet()

	report, err := NewService(host, usage, jails, nil).Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.Host.CPU.CPUs != 16 || report.Host.Load.One != 0.5 {
		t.Errorf("host = %+v", report.Host)
	}
	if report.Configured != 3 {
		t.Errorf("configured = %d, want 3 — stopped jails are still configured", report.Configured)
	}
	if len(report.Jail) != 2 {
		t.Fatalf("reported %d jails, want the 2 that are running: %+v", len(report.Jail), report.Jail)
	}
	// In name order: the port answers with a map, whose iteration order is
	// random, and a dashboard whose rows reshuffle every poll is unreadable.
	if report.Jail[0].Jail != "ottermq" || report.Jail[1].Jail != "postgres" {
		t.Errorf("jails = %q, %q, want them sorted", report.Jail[0].Jail, report.Jail[1].Jail)
	}
	// A stopped jail has no usage to read, and rctl would answer for it with
	// zeros that look like a running jail doing nothing.
	for _, asked := range usage.asked {
		if asked == "stopped-one" {
			t.Error("read usage for a stopped jail")
		}
	}
}

func TestReportReadsTheKernelsAccountingOnce(t *testing.T) {
	host, usage, jails := aHost(), accounting(), aFleet()
	calls := 0
	usage.accountingErr = nil
	wrapped := &countingUsage{stubUsage: usage, calls: &calls}

	if _, err := NewService(host, wrapped, jails, nil).Report(context.Background()); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if calls != 1 {
		t.Errorf("asked whether the host accounts %d times, want 1 for the whole report", calls)
	}
}

type countingUsage struct {
	*stubUsage
	calls *int
}

func (c *countingUsage) RacctEnabled(ctx context.Context) (bool, error) {
	*c.calls++
	return c.stubUsage.RacctEnabled(ctx)
}

// Where the kernel is not accounting, the same quantities are read from ps —
// measured within 1% of rctl's, because racct's memoryuse is the sum of the
// same per-process resident sizes (JD-031).
func TestReportFallsBackToPSWithoutAccounting(t *testing.T) {
	host, usage, jails := aHost(), accounting(), aFleet()
	usage.accounting = false

	report, err := NewService(host, usage, jails, nil).Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(usage.viaPS) != 2 {
		t.Errorf("read %d jails through ps, want 2", len(usage.viaPS))
	}
	for _, jail := range report.Jail {
		if jail.Source != SourcePS {
			t.Errorf("%s reports source %q, want %q", jail.Jail, jail.Source, SourcePS)
		}
		// ps cannot answer these, and the report says so by leaving them out
		// rather than by carrying a zero that reads as a measurement.
		if jail.VMemoryUse != 0 || jail.OpenFiles != 0 || jail.CPUTime != 0 {
			t.Errorf("%s carries rctl-only fields from a ps reading: %+v", jail.Jail, jail)
		}
	}
}

// A dashboard with a gap in it beats one that goes blank because a jail was
// mid-stop when it was asked.
func TestAJailThatCannotBeReadIsReportedRatherThanRaised(t *testing.T) {
	host, usage, jails := aHost(), accounting(), aFleet()
	usage.err = errors.New("rctl: failed to get usage: No such process")

	report, err := NewService(host, usage, jails, nil).Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(report.Jail) != 2 {
		t.Fatalf("reported %d jails, want 2 with their reasons", len(report.Jail))
	}
	for _, jail := range report.Jail {
		if jail.Error == "" {
			t.Errorf("%s was reported without the reason it could not be read", jail.Jail)
		}
	}
	// And the host is still there, which is most of the page.
	if report.Host.CPU.CPUs != 16 {
		t.Error("the host was dropped because a jail failed")
	}
}

// --- the one thing it remembers ----------------------------------------

func TestCPUPercentIsAbsentOnTheFirstReading(t *testing.T) {
	host := aHost()
	service := NewService(host, accounting(), aFleet(), nil)

	report, err := service.Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.Host.CPU.Percent != nil {
		t.Errorf("percent = %v on the first reading, want absent: there is nothing to "+
			"measure against and zero would be a lie", *report.Host.CPU.Percent)
	}
	// The raw counters are always there, so a client that keeps its own
	// baseline is not waiting on ours.
	if report.Host.CPU.Ticks.Total() == 0 {
		t.Error("the counters are missing too")
	}
}

func TestCPUPercentIsTheDeltaBetweenTwoReadings(t *testing.T) {
	host := aHost()
	service := NewService(host, accounting(), aFleet(), nil)

	if _, err := service.Report(context.Background()); err != nil {
		t.Fatalf("the first Report: %v", err)
	}
	// 100 busy ticks and 900 idle since: a tenth of the interval.
	host.ticks = Ticks{User: 180, System: 70, Idle: 1750}

	report, err := service.Report(context.Background())
	if err != nil {
		t.Fatalf("the second Report: %v", err)
	}
	if report.Host.CPU.Percent == nil {
		t.Fatal("percent is absent with two readings to measure between")
	}
	if got := *report.Host.CPU.Percent; got < 9.9 || got > 10.1 {
		t.Errorf("percent = %v, want 10 — 100 busy ticks of a 1000-tick interval", got)
	}
	if report.Host.CPU.OverSeconds == nil || *report.Host.CPU.OverSeconds <= 0 {
		t.Error("over_seconds is missing, so a five-second reading cannot be told " +
			"from a five-minute one")
	}
}

func TestCPUPercentIsAbsentWhenTheHostRebootedUnderUs(t *testing.T) {
	host := aHost()
	service := NewService(host, accounting(), aFleet(), nil)
	if _, err := service.Report(context.Background()); err != nil {
		t.Fatalf("the first Report: %v", err)
	}
	// Counters that went backwards mean a different uptime, and a number
	// derived from two of those is not a measurement of anything.
	host.ticks = Ticks{User: 1, System: 1, Idle: 10}

	report, err := service.Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.Host.CPU.Percent != nil {
		t.Errorf("percent = %v across a reboot", *report.Host.CPU.Percent)
	}
}

func TestCPUPercentIsAbsentWhenNothingMoved(t *testing.T) {
	host := aHost()
	service := NewService(host, accounting(), aFleet(), nil)
	if _, err := service.Report(context.Background()); err != nil {
		t.Fatalf("the first Report: %v", err)
	}

	report, err := service.Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.Host.CPU.Percent != nil {
		t.Errorf("percent = %v with no ticks between the readings", *report.Host.CPU.Percent)
	}
}

// --- refusals ----------------------------------------------------------

func TestReportWithoutHostAccess(t *testing.T) {
	_, err := NewService(nil, accounting(), aFleet(), nil).Report(context.Background())
	if err == nil {
		t.Fatal("want a refusal with no host port wired")
	}
	if code := appCode(t, err); code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", code, http.StatusNotImplemented)
	}
}

// The host is most of the page, and is worth answering on its own.
func TestReportWithoutTheJailPortsIsStillTheHost(t *testing.T) {
	report, err := NewService(aHost(), nil, nil, nil).Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.Host.CPU.CPUs != 16 {
		t.Error("the host was not read")
	}
	if report.Jail == nil {
		t.Error("jails is nil, which marshals to null rather than []")
	}
}

func TestReportFailsWhenTheHostCannotBeRead(t *testing.T) {
	for _, test := range []struct {
		name   string
		break_ func(*stubHost)
	}{
		{"the load average", func(h *stubHost) { h.loadErr = errors.New("no such oid") }},
		{"the cpu counters", func(h *stubHost) { h.ticksErr = errors.New("no such oid") }},
		{"the pools", func(h *stubHost) { h.poolErr = errors.New("pool is suspended") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := aHost()
			test.break_(host)

			_, err := NewService(host, accounting(), aFleet(), nil).Report(context.Background())
			if err == nil {
				t.Fatal("want the error")
			}
			if code := appCode(t, err); code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", code, http.StatusInternalServerError)
			}
		})
	}
}

func TestTicksArithmetic(t *testing.T) {
	ticks := Ticks{User: 10, Nice: 1, System: 5, Interrupt: 2, Idle: 82}
	if got := ticks.Total(); got != 100 {
		t.Errorf("Total() = %d, want 100", got)
	}
	// Busy is everything that is not waiting, interrupt time included.
	if got := ticks.Busy(); got != 18 {
		t.Errorf("Busy() = %d, want 18", got)
	}
}

// --- disks -------------------------------------------------------------

type stubDisks struct {
	disks []Disk
	err   error
	calls int
}

func (s *stubDisks) Disks(context.Context) ([]Disk, error) {
	s.calls++
	return s.disks, s.err
}

var _ DiskSource = (*stubDisks)(nil)

func someDisks() *stubDisks {
	hours := int64(19257)
	return &stubDisks{disks: []Disk{
		{
			Device: "ada3", Kind: DiskATA, Pool: "backup", Vdev: "gpt/backup1",
			Model: "ST500LM000-1EJ162", Passed: true, PowerOnHours: &hours,
			Failing: []Attribute{{ID: 184, Name: "End-to-End_Error",
				Value: 91, Worst: 91, Threshold: 99, Raw: "9"}},
		},
		{Device: "nda0", Kind: DiskNVMe, Pool: "zroot", Vdev: "nda0p4",
			SmartPath: "/dev/nvme0", Passed: true},
	}}
}

func TestReportCarriesDiskHealth(t *testing.T) {
	disks := someDisks()
	report, err := NewService(aHost(), accounting(), aFleet(), disks).
		Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(report.Disks) != 2 {
		t.Fatalf("disks = %+v, want two", report.Disks)
	}
	if disks.calls != 1 {
		t.Errorf("read the disks %d times, want once per report", disks.calls)
	}
	ada3 := report.Disks[0]
	if !ada3.Passed || len(ada3.Failing) != 1 {
		t.Errorf("ada3 = passed %v with %d failing attributes, want passed and one",
			ada3.Passed, len(ada3.Failing))
	}
	// The join is the half neither tool does alone, so it has to survive the
	// trip through the service (JD-032).
	if ada3.Pool != "backup" {
		t.Errorf("ada3 pool = %q, want backup", ada3.Pool)
	}
}

// A smartctl that is unhappy about one device should not cost the operator
// the load average, the pools and every jail on the page (JD-032).
func TestDisksThatCannotBeReadDoNotFailTheReport(t *testing.T) {
	report, err := NewService(aHost(), accounting(), aFleet(),
		&stubDisks{err: errors.New("smartctl: no such device")}).
		Report(context.Background())
	if err != nil {
		t.Fatalf("a failed disk read should not fail the report: %v", err)
	}
	if len(report.Disks) != 0 {
		t.Errorf("disks = %+v, want none", report.Disks)
	}
	if report.Disks == nil {
		t.Error("disks is nil; it should marshal as [] rather than null")
	}
	if len(report.Jail) != 2 || report.Host.CPU.CPUs != 16 {
		t.Errorf("the rest of the report was lost: %d jails, %d cpus",
			len(report.Jail), report.Host.CPU.CPUs)
	}
}

// A Jail Deck built without a disk source at all -- the same shape as a host
// this cannot measure. An empty list, not a nil one.
func TestReportWithoutADiskSource(t *testing.T) {
	report, err := NewService(aHost(), accounting(), aFleet(), nil).
		Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.Disks == nil || len(report.Disks) != 0 {
		t.Errorf("disks = %+v, want an empty list", report.Disks)
	}
}

// Disks are read before the jails, so a host whose jails cannot be listed is
// still a host whose disks can be looked at -- which is exactly the state an
// operator is in when something is wrong.
func TestDisksAreReportedEvenWhenTheJailsCannotBeListed(t *testing.T) {
	report, _ := NewService(aHost(), accounting(),
		&stubJails{err: errors.New("jls: no such file")}, someDisks()).
		Report(context.Background())
	if len(report.Disks) != 2 {
		t.Errorf("disks = %+v, want both despite the jail listing failing", report.Disks)
	}
}
