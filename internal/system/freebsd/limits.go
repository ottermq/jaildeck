package freebsd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
)

// rctlTimeout bounds an rctl call. Each one is a single syscall against the
// kernel's rule list (JD-014).
const rctlTimeout = 15 * time.Second

// racctEnableOID is the loader tunable that turns resource accounting on. A
// kernel built without RACCT does not have it at all.
const racctEnableOID = "kern.racct.enable"

// RacctEnabled reports whether the kernel is accounting resource usage.
//
// A kernel without RACCT has no such oid, and that is an answer rather than
// a failure: there is no tunable to set, and the only honest report is that
// this host cannot carry rctl rules.
func (a *Adapter) RacctEnabled(ctx context.Context) (bool, error) {
	value, err := a.Sysctl(ctx, racctEnableOID)
	if errors.Is(err, host.ErrUnknownOID) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(value) == "1", nil
}

// JailRules lists the rctl rules keyed to a jail's name.
//
//	rctl jail:<name>
//
// The bare filter, not `rctl -l`: measured on 15.1-RELEASE-p5, `rctl -l
// jail:<name>` answers "Operation not supported" for a jail subject, while
// the filter prints the rules and exits 0 with nothing when there are none.
// The filter matches the name exactly -- jail:db1 does not report jail:db10
// -- which is what makes it safe to act on.
func (a *Adapter) JailRules(ctx context.Context, name string) ([]string, error) {
	subject, err := rctlSubject(name)
	if err != nil {
		return nil, err
	}
	result, err := a.runHost(ctx, system.Command{
		Name:    "rctl",
		Args:    []string{subject},
		Timeout: rctlTimeout,
	})
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(result.Stdout), nil
}

// RemoveJailRules clears them.
//
//	rctl -r jail:<name>
//
// A name with no rules exits 1 with "No such process" -- measured -- and
// that is reported as success here. The caller's question is whether any
// rules are left keyed to the name, and none is the answer it wanted; an
// error would make a destroy of an ordinary jail look like a failure.
func (a *Adapter) RemoveJailRules(ctx context.Context, name string) (string, error) {
	subject, err := rctlSubject(name)
	if err != nil {
		return "", err
	}
	cmd := system.Command{
		Name:    "rctl",
		Args:    []string{"-r", subject},
		Timeout: rctlTimeout,
	}
	result, err := a.runHost(ctx, cmd)
	if exited(result, err) && strings.Contains(result.Stderr, "No such process") {
		return cmd.String(), nil
	}
	return cmd.String(), err
}

// checkJailName is the one guard a jail name passes before it becomes part
// of a command here.
//
// An empty name would make an rctl filter of "jail:", which matches every
// jail on the host -- and `rctl -r jail:` would clear every rule on it. A
// colon would end the subject and start a resource. A leading dash is not a
// hazard for rctl, where the name is concatenated into one argument, but it
// is the shape of a flag for every command that takes the name bare, and one
// guard both readers share is worth more than two that disagree.
//
// None of the three can name a jail Jail Deck would create (JD-030), so a
// jail that somehow carries one is refused rather than read wrongly.
func checkJailName(name string) error {
	if name == "" || strings.ContainsAny(name, ": \t\n") || strings.HasPrefix(name, "-") {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%q cannot name a jail here", name))
	}
	return nil
}

// rctlSubject builds the filter a rule or a usage reading is asked for.
func rctlSubject(name string) (string, error) {
	if err := checkJailName(name); err != nil {
		return "", err
	}
	return "jail:" + name, nil
}

// DevfsRulesetNumbers is the jails domain's narrower view of the rulesets
// the kernel has loaded: it needs the numbers to check a spec against, and
// the names are for the readiness report.
func (a *Adapter) DevfsRulesetNumbers(ctx context.Context) ([]int, error) {
	rulesets, err := a.DevfsRulesets(ctx)
	if err != nil {
		return nil, err
	}
	numbers := make([]int, 0, len(rulesets))
	for _, ruleset := range rulesets {
		numbers = append(numbers, ruleset.Number)
	}
	return numbers, nil
}

var (
	_ jails.RctlSystem         = (*Adapter)(nil)
	_ jails.DevfsRulesetSource = (*Adapter)(nil)
)
