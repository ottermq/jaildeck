package freebsd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/firewall"
	"github.com/ottermq/jaildeck/internal/system"
)

// Every byte here is from capture-pf-induced.sh on the VM, which loaded a test
// ruleset, sent a few packets, captured and restored -- or from isengard and
// rohan, where PF is not loaded at all (see testdata/hostfacts/README.md).

const pfSet = "vm-15.1-induced"

// pfCaptures answers every command the firewall view runs against the test
// ruleset: the main ruleset, three anchors, the tables and the log.
var pfCaptures = map[string]string{
	"pfctl -s info":                          "pf_ruleset_info",
	"pfctl -s rules -vv":                     "pf_ruleset_rules_vv",
	"pfctl -s nat -vv":                       "pf_ruleset_nat_vv",
	"pfctl -s Anchors":                       "pf_ruleset_anchors",
	"pfctl -a jaildeck -s rules -vv":         "pf_ruleset_anchor_jaildeck_rules_vv",
	"pfctl -a jaildeck -s nat -vv":           "pf_ruleset_anchor_jaildeck_nat_vv",
	"pfctl -a jaildeck/web -s rules -vv":     "pf_ruleset_anchor_jaildeck_web_rules_vv",
	"pfctl -a jaildeck/web -s nat -vv":       "pf_ruleset_anchor_jaildeck_web_nat_vv",
	"pfctl -a operator -s rules -vv":         "pf_ruleset_anchor_operator_rules_vv",
	"pfctl -a operator -s nat -vv":           "pf_ruleset_anchor_operator_nat_vv",
	"pfctl -s Tables -v":                     "pf_ruleset_tables_v",
	"pfctl -t jaildeck_test_blocked -T show": "pf_ruleset_table_show",
	"pfctl -t jaildeck_test_empty -T show":   "pf_ruleset_table_empty",
	"pfctl -t jaildeck_nosuch -T show":       "pf_ruleset_table_absent",
	"pfctl -a jaildeck/nosuch -s rules -vv":  "pf_ruleset_anchor_absent",
	"sysrc -n pflog_logfile pflog_flags":     "pflog_sysrc",
	"tcpdump -n -e -tttt -r /var/log/pflog":  "pflog_tcpdump",
}

func pfAdapter(t *testing.T, set string, captures map[string]string) (*Adapter, *replayRunner) {
	t.Helper()
	runner := &replayRunner{t: t, set: set, captures: captures}
	return NewAdapter(runner), runner
}

// --- the three states --------------------------------------------------

// Not loaded is what isengard and rohan, the hosts this project actually runs
// on, both answer. It is an answer, not an error (JD-034).
func TestFirewallInfoNotLoadedOnTheRealHosts(t *testing.T) {
	for _, set := range []string{"isengard", "rohan", "vm-15.1"} {
		t.Run(set, func(t *testing.T) {
			a, _ := pfAdapter(t, set, map[string]string{"pfctl -s info": "pfctl_info"})
			info, err := a.FirewallInfo(context.Background())
			if err != nil {
				t.Fatalf("FirewallInfo: %v", err)
			}
			if info.State != firewall.StateNotLoaded {
				t.Errorf("state = %q, want %q", info.State, firewall.StateNotLoaded)
			}
			if info.ForSeconds != nil || info.StateTable != nil {
				t.Errorf("a PF that is not loaded reported figures: %+v", info)
			}
		})
	}
}

// Loaded with a ruleset and not enabled: a policy that is doing nothing, and
// the state most worth telling apart from enabled (JD-034).
func TestFirewallInfoDisabledWithARuleset(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, map[string]string{"pfctl -s info": "pf_ruleset_disabled_info"})
	info, err := a.FirewallInfo(context.Background())
	if err != nil {
		t.Fatalf("FirewallInfo: %v", err)
	}
	if info.State != firewall.StateDisabled {
		t.Errorf("state = %q, want %q", info.State, firewall.StateDisabled)
	}
	if info.ForSeconds == nil {
		t.Error("no duration for a disabled PF, which pfctl does print")
	}
}

func TestFirewallInfoEnabled(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	info, err := a.FirewallInfo(context.Background())
	if err != nil {
		t.Fatalf("FirewallInfo: %v", err)
	}
	if info.State != firewall.StateEnabled {
		t.Fatalf("state = %q, want %q", info.State, firewall.StateEnabled)
	}
	// "Status: Enabled for 0 days 00:00:04"
	if info.ForSeconds == nil || *info.ForSeconds != 4 {
		t.Errorf("for_seconds = %v, want 4", info.ForSeconds)
	}
	// "current entries 2", "searches 8", "inserts 2", "removals 0"
	want := firewall.StateTable{Current: 2, Searches: 8, Inserts: 2, Removals: 0}
	if *info.StateTable != want {
		t.Errorf("state table = %+v, want %+v", *info.StateTable, want)
	}
	if info.Counters["match"] != 2 {
		t.Errorf("counters[match] = %d, want 2", info.Counters["match"])
	}
	if _, rate := info.Counters["0.5/s"]; rate {
		t.Error("a rate was read as a counter name")
	}
}

func TestPFStatusDurationParsesDays(t *testing.T) {
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		return system.CommandResult{Stdout: "Status: Enabled for 4 days 17:02:03           Debug: Urgent\n"}, nil
	}))
	info, err := a.FirewallInfo(context.Background())
	if err != nil {
		t.Fatalf("FirewallInfo: %v", err)
	}
	// Constructed: no capture has been up for days.
	if want := int64(4*86400 + 17*3600 + 2*60 + 3); info.ForSeconds == nil || *info.ForSeconds != want {
		t.Errorf("for_seconds = %v, want %d", info.ForSeconds, want)
	}
}

// --- rules -------------------------------------------------------------

func TestFirewallRulesKeepPfctlsTextAndParseOnlyTheNumbers(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	ruleset, err := a.FirewallRules(context.Background(), "")
	if err != nil {
		t.Fatalf("FirewallRules: %v", err)
	}
	if len(ruleset.Rules) != 7 {
		t.Fatalf("rules = %d, want 7", len(ruleset.Rules))
	}
	first := ruleset.Rules[0]
	// pfctl's own rendering, normalised by pfctl and not re-parsed: the
	// ruleset said `to port 22`, pfctl says `port = ssh`.
	if want := `pass in quick proto tcp from any to any port = ssh flags S/SA keep state label "ssh"`; first.Text != want {
		t.Errorf("text = %q, want %q", first.Text, want)
	}
	if first.Number != 0 || first.Action != "pass" || first.Label != "ssh" {
		t.Errorf("rule 0 = number %d action %q label %q", first.Number, first.Action, first.Label)
	}

	logged := ruleset.Rules[2]
	if logged.Label != "icmp-out-logged" {
		t.Fatalf("rule 2 is %q, want the logged ICMP rule", logged.Label)
	}
	// [ Evaluations: 2  Packets: 6  Bytes: 504  States: 1 ] ... State Creations: 1
	got := []int64{logged.Evaluations, logged.Packets, logged.Bytes, logged.States, logged.StateCreations}
	if !reflect.DeepEqual(got, []int64{2, 6, 504, 1, 1}) {
		t.Errorf("counters = %v, want [2 6 504 1 1]", got)
	}
	if logged.LastActive == "" {
		t.Error("the rule that matched has no Last Active Time")
	}
	if ruleset.Rules[0].LastActive != "" {
		t.Errorf("N/A became %q; it should be absent", ruleset.Rules[0].LastActive)
	}

	block := ruleset.Rules[1]
	if block.Action != "block" {
		t.Errorf("rule 1 action = %q, want block", block.Action)
	}
	anchor := ruleset.Rules[5]
	if anchor.Action != "anchor" || anchor.Text != `anchor "jaildeck/*" all` {
		t.Errorf("rule 5 = %q %q, want the wildcard anchor", anchor.Action, anchor.Text)
	}
}

// -v prints the ALTQ notice to stderr on every call, with exit 0. It is not a
// failure, and not something to report either (JD-034).
func TestTheALTQNoticeIsNeitherAnErrorNorANotice(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	ruleset, err := a.FirewallRules(context.Background(), "")
	if err != nil {
		t.Fatalf("FirewallRules: %v", err)
	}
	if len(ruleset.Notices) != 0 {
		t.Errorf("notices = %v, want the ALTQ lines dropped", ruleset.Notices)
	}
}

// Anything else on stderr with exit 0 is reported rather than swallowed.
func TestAnUnknownStderrLineIsANotice(t *testing.T) {
	a := NewAdapter(runnerFunc(func(_ context.Context, _ system.Command) (system.CommandResult, error) {
		return system.CommandResult{Stderr: "No ALTQ support in kernel\nsomething new\n"}, nil
	}))
	ruleset, err := a.FirewallRules(context.Background(), "")
	if err != nil {
		t.Fatalf("FirewallRules: %v", err)
	}
	if !reflect.DeepEqual(ruleset.Notices, []string{"something new"}) {
		t.Errorf("notices = %v, want [something new]", ruleset.Notices)
	}
}

// -s rules does not show NAT. -s nat does (JD-034).
func TestFirewallNATIsReadSeparately(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	rules, _ := a.FirewallRules(context.Background(), "")
	for _, rule := range rules.Rules {
		if rule.Action == "nat" {
			t.Errorf("-s rules carried a NAT rule after all: %q", rule.Text)
		}
	}
	nat, err := a.FirewallNAT(context.Background(), "")
	if err != nil {
		t.Fatalf("FirewallNAT: %v", err)
	}
	if len(nat.Rules) != 1 || nat.Rules[0].Action != "nat" {
		t.Fatalf("nat = %+v, want the one nat rule", nat.Rules)
	}
	if !strings.Contains(nat.Rules[0].Text, "10.99.0.0/24") {
		t.Errorf("nat text = %q", nat.Rules[0].Text)
	}
}

func TestFirewallAnchorRules(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	web, err := a.FirewallRules(context.Background(), "jaildeck/web")
	if err != nil {
		t.Fatalf("FirewallRules(jaildeck/web): %v", err)
	}
	if len(web.Rules) != 2 || web.Rules[1].Label != "udp-anchored-logged" {
		t.Fatalf("jaildeck/web = %+v", web.Rules)
	}
	// The wildcard's parent exists as an anchor and holds no rules itself.
	parent, err := a.FirewallRules(context.Background(), "jaildeck")
	if err != nil {
		t.Fatalf("FirewallRules(jaildeck): %v", err)
	}
	if len(parent.Rules) != 0 {
		t.Errorf("jaildeck = %+v, want none", parent.Rules)
	}
	if parent.Rules == nil {
		t.Error("an empty ruleset is nil; it should marshal as []")
	}
}

// A nonexistent anchor exits 0. The message is the only sign (JD-034).
func TestANonexistentAnchorIsRecognisedDespiteExitZero(t *testing.T) {
	result, err := replay(t, pfSet, "pf_ruleset_anchor_absent")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("the capture is meant to be exit 0: exit %d, err %v", result.ExitCode, err)
	}
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	if _, err := a.FirewallRules(context.Background(), "jaildeck/nosuch"); !errors.Is(err, firewall.ErrNoSuchAnchor) {
		t.Errorf("err = %v, want ErrNoSuchAnchor", err)
	}
}

// A ruleset in a shape nobody measured is refused, not half-read.
func TestParseRulesRefusesAnUnmeasuredShape(t *testing.T) {
	for _, out := range []string{
		"pass in all\n",                    // no @N: not -vv
		"  [ Evaluations: 1 ]\n",           // counters with no rule
		"@0 pass in all\ncontinued here\n", // a second line of rule text
	} {
		if _, err := parseRules(out); err == nil {
			t.Errorf("parseRules(%q) accepted it", out)
		}
	}
}

func TestParseRulesEmptyIsEmpty(t *testing.T) {
	rules, err := parseRules("")
	if err != nil || rules == nil || len(rules) != 0 {
		t.Errorf("parseRules(\"\") = %v, %v; want an empty, non-nil list", rules, err)
	}
}

// --- anchors and tables ------------------------------------------------

func TestFirewallAnchorsAreFlatAndIncludeInlineAndWildcardParents(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	anchors, err := a.FirewallAnchors(context.Background())
	if err != nil {
		t.Fatalf("FirewallAnchors: %v", err)
	}
	if want := []string{"jaildeck", "jaildeck/web", "operator"}; !reflect.DeepEqual(anchors, want) {
		t.Errorf("anchors = %v, want %v", anchors, want)
	}
}

func TestFirewallTablesFlags(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	tables, err := a.FirewallTables(context.Background())
	if err != nil {
		t.Fatalf("FirewallTables: %v", err)
	}
	want := []firewall.Table{
		{Name: "jaildeck_test_blocked", Flags: "-pa-r--", Persistent: true, Active: true, Referenced: true},
		{Name: "jaildeck_test_empty", Flags: "-pa----", Persistent: true, Active: true},
	}
	if !reflect.DeepEqual(tables, want) {
		t.Errorf("tables = %+v\nwant     %+v", tables, want)
	}
}

// The reason no count is taken from -s Tables -vv: on this capture it says
// one address for a table that -T show lists two in (JD-034). If a recapture
// ever makes them agree, this stops proving why the code does not use it.
func TestTablesVVMiscountsWhichIsWhyItIsNotRead(t *testing.T) {
	vv, _ := replay(t, pfSet, "pf_ruleset_tables_vv")
	if !strings.Contains(vv.Stdout, "Addresses:   1") {
		t.Fatalf("-s Tables -vv no longer says Addresses: 1; re-examine JD-034")
	}
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	addresses, err := a.FirewallTableAddresses(context.Background(), "jaildeck_test_blocked")
	if err != nil {
		t.Fatalf("FirewallTableAddresses: %v", err)
	}
	if len(addresses) != 2 {
		t.Fatalf("addresses = %v, want two", addresses)
	}
}

func TestFirewallTableAddresses(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	addresses, err := a.FirewallTableAddresses(context.Background(), "jaildeck_test_blocked")
	if err != nil {
		t.Fatalf("FirewallTableAddresses: %v", err)
	}
	if want := []string{"192.0.2.10", "198.51.100.0/24"}; !reflect.DeepEqual(addresses, want) {
		t.Errorf("addresses = %v, want %v", addresses, want)
	}
	empty, err := a.FirewallTableAddresses(context.Background(), "jaildeck_test_empty")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("empty table = %v, %v; want an empty, non-nil list", empty, err)
	}
	// Exit 255, "Table does not exist."
	if _, err := a.FirewallTableAddresses(context.Background(), "jaildeck_nosuch"); !errors.Is(err, firewall.ErrNoSuchTable) {
		t.Errorf("err = %v, want ErrNoSuchTable", err)
	}
}

// Every read needs /dev/pf. With PF not loaded each says so as ErrNotLoaded,
// which the service turns into a 409 rather than a 500.
func TestEveryReadKnowsPFIsNotLoaded(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, map[string]string{
		"pfctl -s Anchors":                       "pf_off_anchors",
		"pfctl -s Tables -v":                     "pf_off_tables",
		"pfctl -t jaildeck_test_blocked -T show": "pf_off_table_show",
	})
	ctx := context.Background()
	if _, err := a.FirewallAnchors(ctx); !errors.Is(err, firewall.ErrNotLoaded) {
		t.Errorf("anchors: %v", err)
	}
	if _, err := a.FirewallTables(ctx); !errors.Is(err, firewall.ErrNotLoaded) {
		t.Errorf("tables: %v", err)
	}
	if _, err := a.FirewallTableAddresses(ctx, "jaildeck_test_blocked"); !errors.Is(err, firewall.ErrNotLoaded) {
		t.Errorf("table: %v", err)
	}
}

// --- pflog -------------------------------------------------------------

func TestFirewallLogConfig(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	config, err := a.FirewallLogConfig(context.Background())
	if err != nil {
		t.Fatalf("FirewallLogConfig: %v", err)
	}
	if config.File != "/var/log/pflog" || config.FlushDelaySeconds != 60 {
		t.Errorf("config = %+v, want /var/log/pflog and pflogd's default 60", config)
	}
}

func TestFlushDelay(t *testing.T) {
	for _, tt := range []struct {
		flags string
		want  int
		ok    bool
	}{
		{"", 0, false},
		{"-d 30", 30, true},
		{"-d30", 30, true},
		{"-s 160 -d 5", 5, true},
		{"-d", 0, false},
		{"-d abc", 0, false},
		{"-D", 0, false},
	} {
		got, ok := flushDelay(tt.flags)
		if got != tt.want || ok != tt.ok {
			t.Errorf("flushDelay(%q) = %d, %v; want %d, %v", tt.flags, got, ok, tt.want, tt.ok)
		}
	}
}

func TestFirewallLogParsesBothShapes(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, pfCaptures)
	read, err := a.FirewallLog(context.Background(), "/var/log/pflog")
	if err != nil {
		t.Fatalf("FirewallLog: %v", err)
	}
	if !read.Found || read.Truncated || len(read.Lines) != 2 {
		t.Fatalf("read = %+v, want two lines", read)
	}

	main := read.Lines[0]
	if main.Rule != "2/0" || main.RuleNumber == nil || *main.RuleNumber != 2 || main.Anchor != "" {
		t.Errorf("main-ruleset line = rule %q number %v anchor %q", main.Rule, main.RuleNumber, main.Anchor)
	}
	if main.Action != "pass" || main.Direction != "out" || main.Interface != "vtnet0" {
		t.Errorf("main-ruleset line = %q %q %q", main.Action, main.Direction, main.Interface)
	}
	// -tttt: a date and a time, not -ttt's 00:00:00.
	if !strings.HasPrefix(main.Time, "2026-10-25 ") {
		t.Errorf("time = %q, want an absolute timestamp", main.Time)
	}
	if !strings.HasPrefix(main.Packet, "192.168.122.10 > 192.168.122.1: ICMP echo request") {
		t.Errorf("packet = %q", main.Packet)
	}

	// From inside jaildeck/web: main rule 5, the anchor's LAST component only.
	anchored := read.Lines[1]
	if anchored.Rule != "5.web.1/0" || anchored.RuleNumber == nil || *anchored.RuleNumber != 5 {
		t.Errorf("anchored line = rule %q number %v", anchored.Rule, anchored.RuleNumber)
	}
	if anchored.Anchor != "web" {
		t.Errorf("anchor = %q, want web -- tcpdump does not print jaildeck/web", anchored.Anchor)
	}
}

// A host that has never logged anything has no file. That is an answer.
func TestFirewallLogAbsentFileIsNotAnError(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, map[string]string{
		"tcpdump -n -e -tttt -r /var/log/pflog": "pflog_off_tcpdump",
	})
	read, err := a.FirewallLog(context.Background(), "/var/log/pflog")
	if err != nil {
		t.Fatalf("FirewallLog: %v", err)
	}
	if read.Found || read.Lines == nil || len(read.Lines) != 0 {
		t.Errorf("read = %+v, want not found and an empty list", read)
	}
}

// pflogd buffers. Straight after the packets the file is empty -- measured --
// and that is not an error either; the service says how far behind it can be.
func TestFirewallLogUnflushedIsEmptyNotBroken(t *testing.T) {
	a, _ := pfAdapter(t, pfSet, map[string]string{
		"tcpdump -n -e -tttt -r /var/log/pflog": "pflog_tcpdump_unflushed",
	})
	read, err := a.FirewallLog(context.Background(), "/var/log/pflog")
	if err != nil || !read.Found || len(read.Lines) != 0 {
		t.Errorf("read = %+v, %v; want found and empty", read, err)
	}
}

// Output past the cap is the start of the file, which is the oldest lines.
// No lines rather than old ones under the word "recent" (JD-034).
func TestFirewallLogPastTheCapGivesNoLines(t *testing.T) {
	var asked system.Command
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		asked = cmd
		return system.CommandResult{
			Stdout:          "2026-10-25 18:00:00.000000 rule 2/0(match): pass out on vtnet0: old\n",
			StdoutTruncated: true,
		}, nil
	}))
	read, err := a.FirewallLog(context.Background(), "/var/log/pflog")
	if err != nil {
		t.Fatalf("FirewallLog: %v", err)
	}
	if !read.Truncated || len(read.Lines) != 0 {
		t.Errorf("read = %+v, want truncated and no lines", read)
	}
	if asked.MaxOutput != pflogMaxOutput {
		t.Errorf("MaxOutput = %d, want the pflog cap %d -- the runner's default keeps only 1 MiB",
			asked.MaxOutput, pflogMaxOutput)
	}
}

func TestParseLogLineKeepsWhatItCannotParse(t *testing.T) {
	line := parseLogLine("something tcpdump never printed before")
	if line.Raw != "something tcpdump never printed before" || line.Rule != "" || line.RuleNumber != nil {
		t.Errorf("line = %+v, want only Raw", line)
	}
}

// --- never a write -----------------------------------------------------

// Phase 1 reads (JD-020). Every command any method here can run is checked for
// a flag that changes PF: load, flush, enable, disable, kill states, or a
// table operation other than show (JD-034).
func TestTheFirewallAdapterNeverWrites(t *testing.T) {
	var ran []system.Command
	runner := &replayRunner{t: t, set: pfSet, captures: pfCaptures}
	recording := runnerFunc(func(ctx context.Context, cmd system.Command) (system.CommandResult, error) {
		ran = append(ran, cmd)
		return runner.Run(ctx, cmd)
	})
	a := NewAdapter(recording)
	ctx := context.Background()

	service := firewall.NewService(a)
	if _, err := service.Report(ctx); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if _, err := service.Table(ctx, "jaildeck_test_blocked"); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if _, err := service.Log(ctx, 0); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(ran) < 12 {
		t.Fatalf("only %d commands ran; the check would prove little", len(ran))
	}

	for _, cmd := range ran {
		if cmd.Name != "pfctl" {
			continue
		}
		for i, arg := range cmd.Args {
			switch arg {
			case "-f", "-F", "-e", "-E", "-d", "-X", "-k", "-K", "-z", "-Z":
				t.Errorf("%s writes: %s", cmd, arg)
			case "-T":
				if i+1 >= len(cmd.Args) || cmd.Args[i+1] != "show" {
					t.Errorf("%s is a table operation other than show", cmd)
				}
			}
		}
	}
}
