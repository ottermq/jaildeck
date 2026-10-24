package freebsd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
)

// rctlAdapter answers each command with a result the test set up, and
// records what was asked. A CommandResult rather than an error alone,
// because the adapter reads the exit code and stderr to tell rctl's "there
// were none" apart from a real failure.
type rctlAdapter struct {
	*Adapter
	ran    []string
	answer map[string]system.CommandResult
}

func newRctlAdapter(t *testing.T) *rctlAdapter {
	t.Helper()
	r := &rctlAdapter{answer: map[string]system.CommandResult{}}
	r.Adapter = NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		line := cmd.String()
		r.ran = append(r.ran, line)
		result := r.answer[line]
		if result.ExitCode != 0 {
			return result, errors.New("exit status " + string(rune('0'+result.ExitCode)))
		}
		return result, nil
	}))
	return r
}

// --- accounting --------------------------------------------------------

func TestRacctEnabled(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   bool
	}{
		{name: "on", stdout: "1\n", want: true},
		{name: "off", stdout: "0\n", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := newRctlAdapter(t)
			adapter.answer["sysctl -n kern.racct.enable"] = system.CommandResult{Stdout: test.stdout}

			got, err := adapter.RacctEnabled(context.Background())
			if err != nil {
				t.Fatalf("RacctEnabled: %v", err)
			}
			if got != test.want {
				t.Errorf("enabled = %v, want %v", got, test.want)
			}
		})
	}
}

// A kernel built without RACCT has no such oid. That is an answer about the
// host -- there is no tunable to set -- not a failure to ask.
func TestRacctEnabledOnAKernelWithoutRACCT(t *testing.T) {
	adapter := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{ExitCode: 1, Stderr: "sysctl: unknown oid 'kern.racct.enable'"},
			errors.New("exit status 1")
	}))

	got, err := adapter.RacctEnabled(context.Background())
	if err != nil {
		t.Fatalf("RacctEnabled: %v", err)
	}
	if got {
		t.Error("enabled = true on a kernel with no racct oid")
	}
}

// Anything else is a failure to read the host, and must not be reported as
// "this host does not account", which would turn into a refusal.
func TestRacctEnabledPassesOnARealFailure(t *testing.T) {
	adapter := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{ExitCode: 1, Stderr: "sysctl: permission denied"},
			errors.New("exit status 1")
	}))

	_, err := adapter.RacctEnabled(context.Background())
	if err == nil {
		t.Fatal("want the error, not a false answer")
	}
	if errors.Is(err, host.ErrUnknownOID) {
		t.Errorf("a denied read was reported as a missing oid: %v", err)
	}
}

// --- the rules ---------------------------------------------------------

// The bare filter, not `rctl -l`: measured on 15.1-RELEASE-p5, `rctl -l
// jail:<name>` answers "Operation not supported" for a jail subject.
func TestJailRulesUsesTheFilterForm(t *testing.T) {
	adapter := newRctlAdapter(t)
	adapter.answer["rctl jail:db1"] = system.CommandResult{
		Stdout: "jail:db1:vmemoryuse:deny=1073741824\njail:db1:memoryuse:deny=671088640\n",
	}

	got, err := adapter.JailRules(context.Background(), "db1")
	if err != nil {
		t.Fatalf("JailRules: %v", err)
	}
	want := []string{"jail:db1:vmemoryuse:deny=1073741824", "jail:db1:memoryuse:deny=671088640"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rules = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(adapter.ran, []string{"rctl jail:db1"}) {
		t.Errorf("ran %v", adapter.ran)
	}
}

// No rules is exit 0 with nothing on stdout, which is an answer rather than
// an error -- and an empty list rather than a nil one, so it marshals to [].
func TestJailRulesOnAJailWithNone(t *testing.T) {
	adapter := newRctlAdapter(t)

	got, err := adapter.JailRules(context.Background(), "db1")
	if err != nil {
		t.Fatalf("JailRules: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("rules = %v, want none", got)
	}
	if got == nil {
		t.Error("rules is nil, which marshals to null rather than []")
	}
}

func TestRemoveJailRules(t *testing.T) {
	adapter := newRctlAdapter(t)

	command, err := adapter.RemoveJailRules(context.Background(), "db1")
	if err != nil {
		t.Fatalf("RemoveJailRules: %v", err)
	}
	if command != "rctl -r jail:db1" {
		t.Errorf("command = %q", command)
	}
}

// rctl exits 1 with "No such process" for a name that carries no rules.
// The caller's question is whether any are left, and none is the answer it
// wanted: reporting it as a failure would make an ordinary destroy look
// like a broken one.
func TestRemoveJailRulesTreatsNothingToRemoveAsDone(t *testing.T) {
	adapter := newRctlAdapter(t)
	adapter.answer["rctl -r jail:db1"] = system.CommandResult{
		ExitCode: 1, Stderr: "rctl: failed to remove rule 'jail:db1': No such process",
	}

	if _, err := adapter.RemoveJailRules(context.Background(), "db1"); err != nil {
		t.Fatalf("RemoveJailRules: %v", err)
	}
}

func TestRemoveJailRulesPassesOnARealFailure(t *testing.T) {
	adapter := newRctlAdapter(t)
	adapter.answer["rctl -r jail:db1"] = system.CommandResult{
		ExitCode: 1, Stderr: "rctl: failed to remove rule 'jail:db1': Device busy",
	}

	_, err := adapter.RemoveJailRules(context.Background(), "db1")
	if err == nil {
		t.Fatal("want the error: a rule that will not go is not a rule that was not there")
	}
	if !strings.Contains(err.Error(), "Device busy") {
		t.Errorf("error = %q, want it to carry rctl's own words", err)
	}
}

// An empty name would make the filter "jail:", which matches every jail on
// the host -- and `rctl -r jail:` would clear every rule on it.
func TestRctlRefusesANameThatWouldWidenTheFilter(t *testing.T) {
	for _, name := range []string{"", "db1:memoryuse", "db1 db2", "jail:db1"} {
		adapter := newRctlAdapter(t)

		if _, err := adapter.JailRules(context.Background(), name); err == nil {
			t.Errorf("JailRules accepted %q", name)
		}
		if _, err := adapter.RemoveJailRules(context.Background(), name); err == nil {
			t.Errorf("RemoveJailRules accepted %q", name)
		}
		if len(adapter.ran) != 0 {
			t.Errorf("%q reached the host as %v", name, adapter.ran)
		}
	}
}

// --- devfs rulesets ----------------------------------------------------

func TestDevfsRulesetNumbers(t *testing.T) {
	adapter := newRctlAdapter(t)
	adapter.answer["devfs rule showsets"] = system.CommandResult{Stdout: "1\n2\n3\n4\n5\n"}

	got, err := adapter.DevfsRulesetNumbers(context.Background())
	if err != nil {
		t.Fatalf("DevfsRulesetNumbers: %v", err)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5}) {
		t.Errorf("numbers = %v", got)
	}
}

// --- the stanza, read back ---------------------------------------------

// A stanza Jail Deck writes and cannot read back is a jail that exists on
// disk and appears in no listing (JD-004). The hooks and the parameters are
// both new syntax in that file, so both go through the real parser.
func TestLimitsStanzaRoundTripsThroughTheParser(t *testing.T) {
	adapter := newVNETAdapter(t)
	spec := jails.Spec{
		Name:     "db1",
		Hostname: "db1",
		Template: "tank/jails/templates/15.1-RELEASE@base",
		Network:  jails.Network{Mode: jails.NetworkAlias, IP4: "192.0.2.10/24", Interface: "em0"},
		Limits: jails.Limits{
			MemoryUse: "640M", VMemoryUse: "1G", MaxProc: 100,
			OpenFiles: 2048, PCPU: 100, CPUSet: "0-3",
		},
		Params: map[string]any{
			"devfs_ruleset": 4.0, "securelevel": 3.0, "enforce_statfs": 2.0,
			"children.max": 0.0, "sysvshm": "new", "allow.chflags": true,
		},
	}
	content, err := spec.Stanza(jails.Placement{Path: "/usr/local/jails/containers/db1"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, err := adapter.WriteConfig(context.Background(), "db1", content); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	configured, err := adapter.listConfiguredJails()
	if err != nil {
		t.Fatalf("listConfiguredJails: %v", err)
	}
	if len(configured) != 1 {
		t.Fatalf("parsed %d jails from the stanza just written, want 1: %+v", len(configured), configured)
	}
	jail := configured[0]
	if jail.Name != "db1" {
		t.Errorf("name = %q, want db1", jail.Name)
	}
	if jail.Path != "/usr/local/jails/containers/db1" {
		t.Errorf("path = %q", jail.Path)
	}
	if jail.IP != "192.0.2.10/24" {
		t.Errorf("ip = %q", jail.IP)
	}
	// The hooks are the part most likely to confuse a parser: they repeat a
	// setting with '+=' and carry quoted commands with colons and braces.
	if !strings.Contains(content, `exec.poststart += "cpuset -l 0-3 -j db1";`) {
		t.Fatalf("the stanza under test is not the one intended:\n%s", content)
	}
}
