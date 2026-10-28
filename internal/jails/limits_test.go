package jails

import (
	"context"
	"errors"
	"github.com/ottermq/jaildeck/internal/audit"
	"net/http"
	"strings"
	"testing"
)

// --- doubles -----------------------------------------------------------

// stubRctl is the kernel's rule list. rules is what it holds for a name,
// which is deliberately independent of whether any jail of that name exists:
// that is the fact this whole unit is built around.
type stubRctl struct {
	enabled    bool
	enabledErr error
	rules      map[string][]string
	listErr    error
	removeErr  error

	listed  []string
	removed []string
	steps   *[]string
}

func (s *stubRctl) RacctEnabled(context.Context) (bool, error) {
	return s.enabled, s.enabledErr
}

func (s *stubRctl) JailRules(_ context.Context, name string) ([]string, error) {
	s.listed = append(s.listed, name)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.rules[name], nil
}

func (s *stubRctl) RemoveJailRules(_ context.Context, name string) (string, error) {
	s.removed = append(s.removed, name)
	record(s.steps, "remove-rules")
	if s.removeErr != nil {
		return "", s.removeErr
	}
	delete(s.rules, name)
	return "rctl -r jail:" + name, nil
}

type stubDevfs struct {
	loaded []int
	err    error
	calls  int
}

func (s *stubDevfs) DevfsRulesetNumbers(context.Context) ([]int, error) {
	s.calls++
	return s.loaded, s.err
}

var (
	_ RctlSystem         = (*stubRctl)(nil)
	_ DevfsRulesetSource = (*stubDevfs)(nil)
)

func accountingHost() *stubRctl {
	return &stubRctl{enabled: true, rules: map[string][]string{}}
}

// limitsService is createService with the two ports a jail with limits or
// parameters is judged against.
func limitsService(system *createSystem, cloner *stubCloner, config *stubConfig,
	autostart *stubAutostart, logger *fakeLogger, rctl *stubRctl, devfs *stubDevfs) *JailService {
	return NewJailService(Dependencies{
		System: system, Audit: logger,
		RootCloner: cloner, Config: config, Autostart: autostart,
		Rctl: rctl, DevfsRulesets: devfs,
	})
}

func limitsParts() (*createSystem, *stubCloner, *stubConfig, *stubAutostart, *fakeLogger,
	*stubRctl, *stubDevfs) {
	system := &createSystem{}
	cloner := &stubCloner{root: clonedRoot()}
	config := &stubConfig{path: "/etc/jail.conf.d/db1.conf", system: system}
	return system, cloner, config, &stubAutostart{}, &fakeLogger{},
		accountingHost(), &stubDevfs{loaded: []int{1, 2, 3, 4, 5}}
}

func limitedSpec() Spec {
	spec := validSpec()
	spec.Limits = Limits{MemoryUse: "640M", MaxProc: 100, CPUSet: "0-3"}
	return spec
}

// --- the stanza, measured against a real host --------------------------

// The hooks below are the ones that applied limits to a working jail on
// isengard, 15.1-RELEASE-p5, before this generator existed.
func TestLimitsStanza(t *testing.T) {
	spec := validSpec()
	spec.Limits = Limits{
		MemoryUse: "640M", VMemoryUse: "1G", MaxProc: 100,
		OpenFiles: 2048, PCPU: 100, CPUSet: "0-3",
	}
	got, err := spec.Stanza(Placement{Path: "/usr/local/jails/containers/db1"})
	if err != nil {
		t.Fatalf("Stanza: %v", err)
	}

	want := []string{
		`exec.poststart = "rctl -a jail:db1:memoryuse:deny=640M";`,
		`exec.poststart += "rctl -a jail:db1:vmemoryuse:deny=1G";`,
		`exec.poststart += "rctl -a jail:db1:maxproc:deny=100";`,
		`exec.poststart += "rctl -a jail:db1:openfiles:deny=2048";`,
		// deny, not throttle: rctl(8) describes throttle as the action for
		// %cpu, and `pcpu:throttle` on a jail is "Operation not supported".
		`exec.poststart += "rctl -a jail:db1:pcpu:deny=100";`,
		`exec.poststart += "cpuset -l 0-3 -j db1";`,
		// Without this the rules stay in the kernel keyed to the name, and
		// the next jail called db1 inherits them.
		`exec.prestop = "rctl -r jail:db1";`,
	}
	for _, line := range want {
		if !strings.Contains(got, line) {
			t.Errorf("stanza is missing:\n  %s\ngot:\n%s", line, got)
		}
	}

	// Exactly one assignment per hook; the rest append. A second `=` would
	// silently throw away every rule before it.
	for _, hook := range []string{"exec.poststart = ", "exec.prestop = "} {
		if n := strings.Count(got, hook); n != 1 {
			t.Errorf("stanza has %d %q lines, want exactly 1:\n%s", n, hook, got)
		}
	}
	// The cpuset is pinned after the rules are in force, and the removal is
	// written after the thing it removes.
	assertOrder(t, got, []string{
		`exec.poststart = "rctl -a jail:db1:memoryuse`,
		`exec.poststart += "cpuset -l 0-3 -j db1"`,
		`exec.prestop = "rctl -r jail:db1"`,
	})
}

// A cpuset is the jail's own and dies with it; only rctl rules outlive the
// jail, so only they need taking back.
func TestLimitsStanzaHasNoPrestopForACpusetAlone(t *testing.T) {
	spec := validSpec()
	spec.Limits = Limits{CPUSet: "2"}
	got, err := spec.Stanza(Placement{Path: "/p"})
	if err != nil {
		t.Fatalf("Stanza: %v", err)
	}
	if !strings.Contains(got, `exec.poststart = "cpuset -l 2 -j db1";`) {
		t.Errorf("stanza is missing the cpuset:\n%s", got)
	}
	if strings.Contains(got, "exec.prestop") {
		t.Errorf("stanza removes rules it never added:\n%s", got)
	}
}

func TestStanzaWithoutLimitsHasNoHooks(t *testing.T) {
	got, err := validSpec().Stanza(Placement{Path: "/p"})
	if err != nil {
		t.Fatalf("Stanza: %v", err)
	}
	for _, absent := range []string{"exec.poststart", "exec.prestop", "rctl", "cpuset"} {
		if strings.Contains(got, absent) {
			t.Errorf("stanza mentions %q for a spec with no limits:\n%s", absent, got)
		}
	}
}

func TestParamsStanza(t *testing.T) {
	spec := validSpec()
	spec.Params = map[string]any{
		"devfs_ruleset":      4.0,
		"securelevel":        3.0,
		"enforce_statfs":     2.0,
		"children.max":       0.0,
		"sysvshm":            "new",
		"allow.chflags":      true,
		"allow.set_hostname": false,
	}
	if _, err := spec.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	got, err := spec.Stanza(Placement{Path: "/p"})
	if err != nil {
		t.Fatalf("Stanza: %v", err)
	}
	for _, line := range []string{
		"\tdevfs_ruleset = 4;",
		"\tsecurelevel = 3;",
		"\tenforce_statfs = 2;",
		"\tchildren.max = 0;",
		"\tsysvshm = \"new\";",
		"\tallow.chflags = true;",
		"\tallow.set_hostname = false;",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("stanza is missing:\n  %q\ngot:\n%s", line, got)
		}
	}
	// Sorted, so the same spec writes the same file twice running -- a map
	// would otherwise order them differently on every call.
	assertOrder(t, got, []string{"allow.chflags", "allow.set_hostname", "children.max",
		"devfs_ruleset", "enforce_statfs", "securelevel", "sysvshm"})
}

// --- what a spec may ask for -------------------------------------------

func TestLimitsRefusals(t *testing.T) {
	tests := []struct {
		name    string
		limits  Limits
		message string
	}{
		{
			name:    "a size that is not one",
			limits:  Limits{MemoryUse: "640X"},
			message: "limits.memoryuse",
		},
		{
			name:    "a size with no number",
			limits:  Limits{VMemoryUse: "big"},
			message: "limits.vmemoryuse",
		},
		{
			name:    "a negative count",
			limits:  Limits{MaxProc: -1},
			message: "limits.maxproc cannot be -1",
		},
		{
			name:    "a negative pcpu",
			limits:  Limits{PCPU: -100},
			message: "limits.pcpu",
		},
		{
			// cpuset answers this one with "Resource deadlock avoided",
			// which is why it is caught here instead.
			name:    "a cpu list that is not one",
			limits:  Limits{CPUSet: "abc"},
			message: `"abc" is neither a cpu nor a range`,
		},
		{
			name:    "a backwards range",
			limits:  Limits{CPUSet: "3-0"},
			message: "runs backwards",
		},
		{
			name:    "a range with a stray item",
			limits:  Limits{CPUSet: "0-3,"},
			message: "limits.cpuset",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validSpec()
			spec.Limits = test.limits
			_, err := spec.normalize()
			if err == nil {
				t.Fatalf("want a refusal for %+v", test.limits)
			}
			if code := appCode(t, err); code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Errorf("error = %q, want it to mention %q", err, test.message)
			}
		})
	}
}

func TestLimitsAccepts(t *testing.T) {
	for _, limits := range []Limits{
		{MemoryUse: "671088640"},
		{MemoryUse: "640m"},
		{VMemoryUse: "1T"},
		{CPUSet: "0"},
		{CPUSet: "0-3,8,10-11"},
		{PCPU: 1600},
	} {
		spec := validSpec()
		spec.Limits = limits
		if _, err := spec.normalize(); err != nil {
			t.Errorf("%+v: %v", limits, err)
		}
	}
}

func TestParamRefusals(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		message string
	}{
		{
			name:    "a parameter nobody allowed",
			params:  map[string]any{"allow.chflags2": true},
			message: "params.allow.chflags2 is not a jail parameter Jail Deck will set",
		},
		{
			// JD-018 names this one, so the refusal says it was decided.
			name:    "mounting",
			params:  map[string]any{"allow.mount": true},
			message: "params.allow.mount is never allowed",
		},
		{
			name:    "mounting something in particular",
			params:  map[string]any{"allow.mount.devfs": true},
			message: "is never allowed",
		},
		{
			name:    "raw sockets",
			params:  map[string]any{"allow.raw_sockets": true},
			message: "params.allow.raw_sockets is never allowed",
		},
		{
			name:    "the host's IPC",
			params:  map[string]any{"allow.sysvipc": true},
			message: `"sysvmsg", "sysvsem" and "sysvshm" set to "new"`,
		},
		{
			name:    "nested jails",
			params:  map[string]any{"children.max": 4.0},
			message: "params.children.max has to be between 0 and 0",
		},
		{
			name:    "the host's IPC by another route",
			params:  map[string]any{"sysvshm": "inherit"},
			message: `params.sysvshm cannot be "inherit"`,
		},
		{
			name:    "a path of its own",
			params:  map[string]any{"path": "/elsewhere"},
			message: "written by Jail Deck itself",
		},
		{
			name:    "its own networking",
			params:  map[string]any{"vnet": true},
			message: `use "network.mode": "vnet"`,
		},
		{
			name:    "its own hooks",
			params:  map[string]any{"exec.poststart": "curl evil"},
			message: "params.exec.poststart is written by Jail Deck itself",
		},
		{
			name:    "a securelevel out of range",
			params:  map[string]any{"securelevel": 9.0},
			message: "params.securelevel has to be between -1 and 3",
		},
		{
			name:    "a ruleset that is not whole",
			params:  map[string]any{"devfs_ruleset": 4.5},
			message: "params.devfs_ruleset has to be a whole number",
		},
		{
			name:    "a number sent as a word",
			params:  map[string]any{"devfs_ruleset": "4"},
			message: "params.devfs_ruleset has to be a whole number",
		},
		{
			name:    "a flag sent as a number",
			params:  map[string]any{"allow.chflags": 1.0},
			message: "params.allow.chflags has to be true or false",
		},
		{
			name:    "a word sent as a flag",
			params:  map[string]any{"sysvsem": true},
			message: `params.sysvsem has to be one of "disable" or "new"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validSpec()
			spec.Params = test.params
			_, err := spec.normalize()
			if err == nil {
				t.Fatalf("want a refusal for %v", test.params)
			}
			if code := appCode(t, err); code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Errorf("error = %q, want it to mention %q", err, test.message)
			}
		})
	}
}

// The allowlist's whole point is that it fails closed, so the refusal has to
// be the default rather than a list of known-bad names.
func TestEveryParameterIsRefusedUnlessAllowed(t *testing.T) {
	for _, name := range []string{
		"allow.mlock", "allow.socket_af", "allow.quotas", "allow.suser",
		"allow.nfsd", "osrelease", "linux", "ip4.saddrsel", "enforce_statfs ",
	} {
		spec := validSpec()
		spec.Params = map[string]any{name: true}
		if _, err := spec.normalize(); err == nil {
			t.Errorf("params.%s was accepted; the allowlist has to fail closed", name)
		}
	}
}

// --- what the host is asked before anything is created -----------------

func TestCreateRefusesRctlLimitsWithoutAccounting(t *testing.T) {
	system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
	rctl.enabled = false
	service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)

	result, err := service.Create(context.Background(), limitedSpec())
	if err == nil {
		t.Fatal("want a refusal on a host that is not accounting")
	}
	if code := appCode(t, err); code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
	}
	if !strings.Contains(err.Error(), "kern.racct.enable") {
		t.Errorf("error = %q, want it to name the tunable", err)
	}
	if result.FailedAt != StepCheck {
		t.Errorf("failed_at = %q, want %q", result.FailedAt, StepCheck)
	}
	if len(cloner.cloned) != 0 {
		t.Errorf("cloned %v after refusing the spec", cloner.cloned)
	}
}

// Each rctl field makes a jail need accounting on its own.
//
// Asserted one field at a time because every other test here sets several at
// once, and any one of them dropping out of the question is then hidden by
// the rest -- which is exactly what a fault injection found.
func TestEveryRctlLimitNeedsAccountingOnItsOwn(t *testing.T) {
	tests := []struct {
		resource string
		limits   Limits
	}{
		{"memoryuse", Limits{MemoryUse: "640M"}},
		{"vmemoryuse", Limits{VMemoryUse: "1G"}},
		{"maxproc", Limits{MaxProc: 100}},
		{"openfiles", Limits{OpenFiles: 2048}},
		{"pcpu", Limits{PCPU: 100}},
	}
	for _, test := range tests {
		t.Run(test.resource, func(t *testing.T) {
			if !test.limits.rctl() {
				t.Errorf("%+v does not count as an rctl limit", test.limits)
			}
			rules := test.limits.Rules("db1")
			if len(rules) != 1 || !strings.Contains(rules[0], ":"+test.resource+":") {
				t.Errorf("rules = %v, want one for %s", rules, test.resource)
			}

			system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
			rctl.enabled = false
			service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)
			spec := validSpec()
			spec.Limits = test.limits

			_, err := service.Create(context.Background(), spec)
			if err == nil {
				t.Fatalf("a host that is not accounting took %+v", test.limits)
			}
			if code := appCode(t, err); code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
			}
			if len(cloner.cloned) != 0 {
				t.Errorf("cloned %v after refusing the spec", cloner.cloned)
			}
		})
	}
}

// And nothing else does: a cpuset needs no accounting, and an empty Limits
// must not make a jail look as though it asked for anything.
func TestWhatDoesNotNeedAccounting(t *testing.T) {
	for _, limits := range []Limits{{}, {CPUSet: "0-3"}} {
		if limits.rctl() {
			t.Errorf("%+v counts as an rctl limit", limits)
		}
		if rules := limits.Rules("db1"); len(rules) != 0 {
			t.Errorf("%+v generates %v", limits, rules)
		}
	}
}

// A cpuset is not an rctl rule and needs no accounting, so the one limit a
// host without RACCT can still have is not taken away from it.
func TestCreateAllowsACpusetWithoutAccounting(t *testing.T) {
	system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
	rctl.enabled = false
	service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)

	spec := validSpec()
	spec.Limits = Limits{CPUSet: "0-3"}
	if _, err := service.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(config.written["db1"], `cpuset -l 0-3 -j db1`) {
		t.Errorf("stanza has no cpuset:\n%s", config.written["db1"])
	}
}

// The trap this check exists for: the rules are keyed to the name, not to
// the jail, so a new jail of an old name runs under limits nobody asked for.
func TestCreateRefusesANameWhoseRulesAreStillInTheKernel(t *testing.T) {
	system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
	rctl.rules["db1"] = []string{"jail:db1:memoryuse:deny=64M"}
	service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)

	// A spec with no limits at all: this is the dangerous case, because
	// nothing about it suggests the jail would be limited.
	result, err := service.Create(context.Background(), validSpec())
	if err == nil {
		t.Fatal("want a refusal for a name with rules left in the kernel")
	}
	if code := appCode(t, err); code != http.StatusConflict {
		t.Errorf("status = %d, want %d", code, http.StatusConflict)
	}
	for _, want := range []string{"jail:db1:memoryuse:deny=64M", "rctl -r jail:db1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
	if result.FailedAt != StepCheck {
		t.Errorf("failed_at = %q, want %q", result.FailedAt, StepCheck)
	}
	if len(cloner.cloned) != 0 {
		t.Errorf("cloned %v after refusing the spec", cloner.cloned)
	}
	// Refused, not cleared. Those rules are somebody's, and Jail Deck
	// destroys nothing it did not make.
	if len(rctl.removed) != 0 {
		t.Errorf("cleared rules it did not create: %v", rctl.removed)
	}
}

func TestCreateRefusesADevfsRulesetTheKernelDoesNotHave(t *testing.T) {
	system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
	service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)

	spec := validSpec()
	spec.Params = map[string]any{"devfs_ruleset": 99.0}
	result, err := service.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("want a refusal for a ruleset the kernel has not loaded")
	}
	if code := appCode(t, err); code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
	}
	for _, want := range []string{"no devfs ruleset 99", "1, 2, 3, 4, 5", "whole /dev"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
	if result.FailedAt != StepCheck {
		t.Errorf("failed_at = %q, want %q", result.FailedAt, StepCheck)
	}
	if len(cloner.cloned) != 0 {
		t.Errorf("cloned %v after refusing the spec", cloner.cloned)
	}
}

func TestCreateAcceptsADevfsRulesetTheKernelHas(t *testing.T) {
	system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
	service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)

	spec := validSpec()
	spec.Params = map[string]any{"devfs_ruleset": 4.0}
	if _, err := service.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(config.written["db1"], "devfs_ruleset = 4;") {
		t.Errorf("stanza has no ruleset:\n%s", config.written["db1"])
	}
}

// A spec that names no ruleset gets the restricted /dev the kernel gives a
// jail anyway, so there is nothing to check and no reason to ask.
func TestCreateDoesNotAskAboutRulesetsItWasNotGiven(t *testing.T) {
	system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
	service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)

	if _, err := service.Create(context.Background(), validSpec()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if devfs.calls != 0 {
		t.Errorf("read the kernel's rulesets %d times for a spec that named none", devfs.calls)
	}
}

// Without the ports the checks cannot be made, and a Jail Deck that cannot
// check still creates jails -- the same position checkBridge takes.
func TestCreateWorksWithoutTheLimitPorts(t *testing.T) {
	system := &createSystem{}
	cloner := &stubCloner{root: clonedRoot()}
	config := &stubConfig{path: "/etc/jail.conf.d/db1.conf", system: system}
	service := createService(system, cloner, config, &stubAutostart{}, &fakeLogger{})

	if _, err := service.Create(context.Background(), limitedSpec()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(config.written["db1"], "rctl -a jail:db1:memoryuse:deny=640M") {
		t.Errorf("stanza has no rules:\n%s", config.written["db1"])
	}
}

// --- a start that could not be confirmed -------------------------------

// jail(8) destroys a jail whose poststart fails and never runs its prestop,
// so whatever rules it had already added stay in the kernel.
func TestFailedStartTakesBackTheRulesItApplied(t *testing.T) {
	system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
	system.startErr = errors.New("jail status is 'stopped', want 'running'")
	rctl.rules["db1"] = []string{"jail:db1:memoryuse:deny=640M"}
	service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)

	// The create's own check runs before the start, so the rules have to
	// arrive the way the host would have added them: during it.
	delete(rctl.rules, "db1")
	cloner.root = clonedRoot()
	system.existing = nil
	rctlAddsOnStart(system, rctl)

	result, err := service.Create(context.Background(), limitedSpec())
	if err == nil {
		t.Fatal("want the start's error")
	}
	if result.FailedAt != StepStart {
		t.Errorf("failed_at = %q, want %q", result.FailedAt, StepStart)
	}
	if len(result.RulesRemoved) != 1 || result.RulesRemoved[0] != "jail:db1:memoryuse:deny=640M" {
		t.Errorf("rules_removed = %v, want the rule the start applied", result.RulesRemoved)
	}
	if result.RulesError != "" {
		t.Errorf("rules_error = %q, want none", result.RulesError)
	}
	if len(rctl.rules["db1"]) != 0 {
		t.Errorf("left %v in the kernel", rctl.rules["db1"])
	}
	wantSweep(t, logger, OperationRemoveLeftoverRules, &audit.Entry{Command: "rctl -r jail:db1", Success: true})
}

// rctlAddsOnStart makes the fake host behave like jail(8): the stanza's
// poststart rules land in the kernel as the jail comes up, including when
// the start then fails.
func rctlAddsOnStart(system *createSystem, rctl *stubRctl) {
	system.onStart = func(name string) {
		rctl.rules[name] = []string{"jail:" + name + ":memoryuse:deny=640M"}
	}
}

// A sweep that fails is reported with the command that fixes it, because the
// next create of this name is what will otherwise refuse, days later.
func TestFailedStartReportsRulesItCouldNotTakeBack(t *testing.T) {
	system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
	system.startErr = errors.New("jail status is 'stopped', want 'running'")
	rctl.removeErr = errors.New("rctl: failed to remove rule: Device busy")
	rctlAddsOnStart(system, rctl)
	service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)

	result, err := service.Create(context.Background(), limitedSpec())
	if err == nil {
		t.Fatal("want the start's error")
	}
	if len(result.RulesRemoved) != 0 {
		t.Errorf("rules_removed = %v, want none", result.RulesRemoved)
	}
	for _, want := range []string{"rctl -r jail:db1", "Device busy"} {
		if !strings.Contains(result.RulesError, want) {
			t.Errorf("rules_error = %q, want it to mention %q", result.RulesError, want)
		}
	}
	wantSweep(t, logger, OperationRemoveLeftoverRules, &audit.Entry{Command: RemoveRulesCommand("db1"), Success: false})
}

// Nothing was applied, so nothing is swept -- and the kernel is not asked.
func TestFailedStartWithoutRctlLimitsSweepsNothing(t *testing.T) {
	system, cloner, config, autostart, logger, rctl, devfs := limitsParts()
	system.startErr = errors.New("jail status is 'stopped', want 'running'")
	service := limitsService(system, cloner, config, autostart, logger, rctl, devfs)

	spec := validSpec()
	spec.Limits = Limits{CPUSet: "0-3"}
	before := len(rctl.listed)
	if _, err := service.Create(context.Background(), spec); err == nil {
		t.Fatal("want the start's error")
	}
	if len(rctl.listed) != before+1 {
		t.Errorf("asked the kernel %d times, want only the create's own check",
			len(rctl.listed)-before)
	}
	if len(rctl.removed) != 0 {
		t.Errorf("removed %v for a jail with no rules", rctl.removed)
	}
}

// --- destroy -----------------------------------------------------------

// destroyWithRules is destroyable() plus the kernel's rule list, which is
// the only part of a destroy this unit changes.
type destroyWithRules struct {
	system    *createSystem
	destroyer *stubDestroyer
	remover   *stubRemover
	autostart *stubAutostart
	logger    *fakeLogger
	rctl      *stubRctl
}

func destroyableWithRules() *destroyWithRules {
	system, destroyer, remover, autostart, logger := destroyable()
	return &destroyWithRules{
		system: system, destroyer: destroyer, remover: remover,
		autostart: autostart, logger: logger, rctl: accountingHost(),
	}
}

func (d *destroyWithRules) service() *JailService {
	return NewJailService(Dependencies{
		System: d.system, Audit: d.logger,
		RootDestroyer: d.destroyer, ConfigRemover: d.remover, Autostart: d.autostart,
		Rctl: d.rctl,
	})
}

func TestDestroySweepsRulesLeftBehind(t *testing.T) {
	parts := destroyableWithRules()
	parts.rctl.rules["db1"] = []string{"jail:db1:maxproc:deny=100"}

	result, err := parts.service().Destroy(context.Background(), "db1", "db1")
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if !result.Destroyed {
		t.Fatalf("destroy did not finish: %+v", result)
	}
	if len(result.Rules) != 1 || result.Rules[0] != "jail:db1:maxproc:deny=100" {
		t.Errorf("rules = %v, want the leftover rule", result.Rules)
	}
	if len(parts.rctl.rules["db1"]) != 0 {
		t.Errorf("left %v in the kernel", parts.rctl.rules["db1"])
	}
	wantSweep(t, parts.logger, OperationRemoveLeftoverRules, &audit.Entry{Command: "rctl -r jail:db1", Success: true})
}

// The ordinary case: the jail's own prestop already took them away.
func TestDestroyReportsNoRulesWhenThereAreNone(t *testing.T) {
	parts := destroyableWithRules()
	result, err := parts.service().Destroy(context.Background(), "db1", "db1")
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(result.Rules) != 0 {
		t.Errorf("rules = %v, want none", result.Rules)
	}
	if len(parts.rctl.removed) != 0 {
		t.Errorf("ran a removal with nothing to remove: %v", parts.rctl.removed)
	}
	wantSweep(t, parts.logger, OperationRemoveLeftoverRules, nil)
}

// The jail and its dataset are the point. A rule that will not go is
// reported, not raised.
func TestDestroyFinishesWhenTheRulesWillNotGo(t *testing.T) {
	parts := destroyableWithRules()
	parts.rctl.rules["db1"] = []string{"jail:db1:maxproc:deny=100"}
	parts.rctl.removeErr = errors.New("rctl: failed to remove rule: Device busy")

	result, err := parts.service().Destroy(context.Background(), "db1", "db1")
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if !result.Destroyed {
		t.Errorf("destroy stopped over a leftover rule: %+v", result)
	}
	if !strings.Contains(result.RulesError, "Device busy") {
		t.Errorf("rules_error = %q, want it to say why", result.RulesError)
	}
	wantSweep(t, parts.logger, OperationRemoveLeftoverRules, &audit.Entry{Command: RemoveRulesCommand("db1"), Success: false})
}

// The sweep runs after the jail is down, so it cannot be racing a prestop
// that is still to come.
func TestDestroySweepsRulesAfterTheStop(t *testing.T) {
	parts := destroyableWithRules()
	steps := []string{}
	parts.system.steps = &steps
	parts.rctl.steps = &steps
	parts.rctl.rules["db1"] = []string{"jail:db1:maxproc:deny=100"}

	if _, err := parts.service().Destroy(context.Background(), "db1", "db1"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	assertSteps(t, steps, []string{"stop", "remove-rules"})
}

func assertSteps(t *testing.T, got, want []string) {
	t.Helper()
	at := 0
	for _, step := range got {
		if at < len(want) && step == want[at] {
			at++
		}
	}
	if at != len(want) {
		t.Errorf("steps %v do not contain %v in order", got, want)
	}
}
