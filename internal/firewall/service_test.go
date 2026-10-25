package firewall

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

type stubSystem struct {
	info      Info
	infoErr   error
	rules     map[string]Ruleset
	nat       map[string]Ruleset
	ruleErr   map[string]error
	anchors   []string
	tables    []Table
	addresses map[string][]string
	tableErr  error
	config    LogConfig
	log       LogRead
	calls     []string
}

func (s *stubSystem) FirewallInfo(context.Context) (Info, error) {
	s.calls = append(s.calls, "info")
	return s.info, s.infoErr
}

func (s *stubSystem) FirewallRules(_ context.Context, anchor string) (Ruleset, error) {
	s.calls = append(s.calls, "rules "+anchor)
	if err := s.ruleErr[anchor]; err != nil {
		return Ruleset{}, err
	}
	return s.rules[anchor], nil
}

func (s *stubSystem) FirewallNAT(_ context.Context, anchor string) (Ruleset, error) {
	s.calls = append(s.calls, "nat "+anchor)
	return s.nat[anchor], nil
}

func (s *stubSystem) FirewallAnchors(context.Context) ([]string, error) {
	s.calls = append(s.calls, "anchors")
	return s.anchors, nil
}

func (s *stubSystem) FirewallTables(context.Context) ([]Table, error) {
	s.calls = append(s.calls, "tables")
	return s.tables, s.tableErr
}

func (s *stubSystem) FirewallTableAddresses(_ context.Context, name string) ([]string, error) {
	s.calls = append(s.calls, "table "+name)
	return s.addresses[name], nil
}

func (s *stubSystem) FirewallLogConfig(context.Context) (LogConfig, error) {
	return s.config, nil
}

func (s *stubSystem) FirewallLog(context.Context, string) (LogRead, error) {
	return s.log, nil
}

var _ FirewallSystem = (*stubSystem)(nil)

func appCode(t *testing.T, err error) int {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("want a *common.AppError, got %T: %v", err, err)
	}
	return appErr.Code
}

func loaded(state string) *stubSystem {
	seconds := int64(4)
	return &stubSystem{
		info: Info{State: state, ForSeconds: &seconds, StateTable: &StateTable{Current: 2},
			Counters: map[string]int64{"match": 2}},
		rules: map[string]Ruleset{
			"":             {Rules: []Rule{{Number: 0, Text: "pass in all", Action: "pass"}}},
			"jaildeck/web": {Rules: []Rule{{Number: 0, Text: `pass in proto tcp to port = http`, Action: "pass"}}},
		},
		nat: map[string]Ruleset{
			"": {Rules: []Rule{{Number: 0, Text: "nat on vtnet0 …", Action: "nat"}}},
		},
		anchors:   []string{"jaildeck", "jaildeck/web"},
		tables:    []Table{{Name: "blocked", Flags: "-pa-r--", Persistent: true, Active: true, Referenced: true}},
		addresses: map[string][]string{"blocked": {"192.0.2.10", "198.51.100.0/24"}},
		config:    LogConfig{File: "/var/log/pflog", FlushDelaySeconds: 60},
	}
}

// --- the report --------------------------------------------------------

// Not loaded is the whole answer, a 200, and nothing else is asked of a PF
// that is not there (JD-034).
func TestReportWhenPFIsNotLoaded(t *testing.T) {
	system := &stubSystem{info: Info{State: StateNotLoaded}}
	report, err := NewService(system).Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.State != StateNotLoaded {
		t.Errorf("state = %q", report.State)
	}
	if len(system.calls) != 1 {
		t.Errorf("calls = %v, want only the info read", system.calls)
	}
	// Lists, not nulls: a client iterating them must not have to check.
	if report.Rules == nil || report.NAT == nil || report.Anchors == nil || report.Tables == nil {
		t.Errorf("a not-loaded report has nil lists: %+v", report)
	}
}

// Disabled with a ruleset reads exactly like enabled -- everything is there
// -- and the state is what says the rules are not being applied.
func TestReportWhenPFIsDisabledStillShowsTheRules(t *testing.T) {
	report, err := NewService(loaded(StateDisabled)).Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.State != StateDisabled {
		t.Errorf("state = %q, want disabled", report.State)
	}
	if len(report.Rules) != 1 || len(report.NAT) != 1 || len(report.Anchors) != 2 || len(report.Tables) != 1 {
		t.Errorf("a disabled PF lost part of its policy: %+v", report)
	}
}

func TestReportReadsEveryAnchorsRulesAndNAT(t *testing.T) {
	report, err := NewService(loaded(StateEnabled)).Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if *report.ForSeconds != 4 || report.StateTable.Current != 2 || report.Counters["match"] != 2 {
		t.Errorf("info lost on the way: %+v", report)
	}
	web := report.Anchors[1]
	if web.Name != "jaildeck/web" || len(web.Rules) != 1 {
		t.Errorf("anchor = %+v, want jaildeck/web with its rule", web)
	}
	// An anchor with nothing in it is still an anchor, with empty lists.
	if parent := report.Anchors[0]; parent.Rules == nil || parent.NAT == nil {
		t.Errorf("an empty anchor has nil lists: %+v", parent)
	}
}

// An anchor that vanished between being listed and being read is reported
// with its reason; the rest of the page still stands.
func TestAnAnchorThatCannotBeReadDoesNotFailTheReport(t *testing.T) {
	system := loaded(StateEnabled)
	system.ruleErr = map[string]error{"jaildeck/web": ErrNoSuchAnchor}
	report, err := NewService(system).Report(context.Background())
	if err != nil {
		t.Fatalf("one anchor failed the whole report: %v", err)
	}
	if web := report.Anchors[1]; web.Error == "" {
		t.Errorf("anchor = %+v, want its error", web)
	}
	if len(report.Rules) != 1 || len(report.Tables) != 1 {
		t.Error("the rest of the report was lost")
	}
}

// The main ruleset is the point of the page; failing to read it is a 500,
// not an empty list that would look like "no rules".
func TestTheMainRulesetFailingFailsTheReport(t *testing.T) {
	system := loaded(StateEnabled)
	system.ruleErr = map[string]error{"": errors.New("pfctl exploded")}
	_, err := NewService(system).Report(context.Background())
	if err == nil || appCode(t, err) != http.StatusInternalServerError {
		t.Fatalf("err = %v, want a 500", err)
	}
}

func TestNoticesAreCollected(t *testing.T) {
	system := loaded(StateEnabled)
	main := system.rules[""]
	main.Notices = []string{"something new"}
	system.rules[""] = main
	report, _ := NewService(system).Report(context.Background())
	if len(report.Notices) != 1 || report.Notices[0] != "something new" {
		t.Errorf("notices = %v", report.Notices)
	}
}

func TestWithoutHostAccessIsA501(t *testing.T) {
	service := NewService(nil)
	for name, call := range map[string]func() error{
		"report": func() error { _, err := service.Report(context.Background()); return err },
		"table":  func() error { _, err := service.Table(context.Background(), "t"); return err },
		"log":    func() error { _, err := service.Log(context.Background(), 0); return err },
	} {
		if err := call(); err == nil || appCode(t, err) != http.StatusNotImplemented {
			t.Errorf("%s: %v, want 501", name, err)
		}
	}
}

// --- tables ------------------------------------------------------------

func TestTableReadsAListedTable(t *testing.T) {
	contents, err := NewService(loaded(StateEnabled)).Table(context.Background(), "blocked")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if contents.Name != "blocked" || len(contents.Addresses) != 2 {
		t.Errorf("contents = %+v", contents)
	}
}

// Only a name -s Tables listed is read. That is the gate, and it makes "no
// such table" Jail Deck's 404 rather than pfctl's exit 255.
func TestTableThatIsNotListedIs404AndIsNeverRead(t *testing.T) {
	system := loaded(StateEnabled)
	_, err := NewService(system).Table(context.Background(), "nosuch")
	if err == nil || appCode(t, err) != http.StatusNotFound {
		t.Fatalf("err = %v, want 404", err)
	}
	for _, call := range system.calls {
		if call == "table nosuch" {
			t.Error("an unlisted table was read anyway")
		}
	}
}

func TestTableNameValidation(t *testing.T) {
	for _, name := range []string{"", "-Fall", "has space", "tab\there", "nul\x00", "0123456789012345678901234567890123"} {
		system := loaded(StateEnabled)
		_, err := NewService(system).Table(context.Background(), name)
		if err == nil || appCode(t, err) != http.StatusBadRequest {
			t.Errorf("Table(%q) = %v, want 400", name, err)
		}
		if len(system.calls) != 0 {
			t.Errorf("Table(%q) reached the host: %v", name, system.calls)
		}
	}
	// 31 bytes is the longest PF allows, and is accepted as far as the name.
	if err := validTableName("0123456789012345678901234567890"); err != nil {
		t.Errorf("a 31-byte name was refused: %v", err)
	}
}

func TestTableWhenPFIsNotLoadedIs409(t *testing.T) {
	system := loaded(StateEnabled)
	system.tableErr = ErrNotLoaded
	_, err := NewService(system).Table(context.Background(), "blocked")
	if err == nil || appCode(t, err) != http.StatusConflict {
		t.Fatalf("err = %v, want 409", err)
	}
}

// --- the log -----------------------------------------------------------

func logLines(n int) []LogLine {
	lines := make([]LogLine, n)
	for i := range lines {
		lines[i] = LogLine{Raw: string(rune('a' + i%26))}
	}
	return lines
}

func TestLogKeepsTheMostRecentLines(t *testing.T) {
	system := loaded(StateEnabled)
	all := logLines(150)
	system.log = LogRead{Found: true, Lines: all}
	report, err := NewService(system).Log(context.Background(), 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(report.Lines) != DefaultLogLines {
		t.Fatalf("lines = %d, want the default %d", len(report.Lines), DefaultLogLines)
	}
	// The tail, not the head: the file is oldest first.
	if report.Lines[0] != all[50] || report.Lines[99] != all[149] {
		t.Error("the lines kept are not the last ones")
	}
	if report.FlushDelaySeconds != 60 || report.File != "/var/log/pflog" {
		t.Errorf("report = %+v", report)
	}
}

func TestLogFewerLinesThanAskedIsAllOfThem(t *testing.T) {
	system := loaded(StateEnabled)
	system.log = LogRead{Found: true, Lines: logLines(3)}
	report, _ := NewService(system).Log(context.Background(), 50)
	if len(report.Lines) != 3 {
		t.Errorf("lines = %d, want 3", len(report.Lines))
	}
}

// Cut short means the lines read are the oldest. None rather than those.
func TestLogTruncatedGivesNoLines(t *testing.T) {
	system := loaded(StateEnabled)
	system.log = LogRead{Found: true, Truncated: true, Lines: logLines(5)}
	report, err := NewService(system).Log(context.Background(), 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if !report.Truncated || len(report.Lines) != 0 {
		t.Errorf("report = %+v, want truncated with no lines", report)
	}
}

func TestLogNotFoundIsAnAnswer(t *testing.T) {
	system := loaded(StateEnabled)
	system.log = LogRead{Found: false}
	report, err := NewService(system).Log(context.Background(), 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if report.Found || report.Lines == nil || len(report.Lines) != 0 {
		t.Errorf("report = %+v, want not found and an empty list", report)
	}
}

func TestLogLineLimits(t *testing.T) {
	for _, n := range []int{-1, MaxLogLines + 1} {
		_, err := NewService(loaded(StateEnabled)).Log(context.Background(), n)
		if err == nil || appCode(t, err) != http.StatusBadRequest {
			t.Errorf("Log(%d) = %v, want 400", n, err)
		}
	}
	system := loaded(StateEnabled)
	system.log = LogRead{Found: true, Lines: logLines(MaxLogLines + 5)}
	report, err := NewService(system).Log(context.Background(), MaxLogLines)
	if err != nil || len(report.Lines) != MaxLogLines {
		t.Errorf("Log(max) = %d lines, %v", len(report.Lines), err)
	}
}
