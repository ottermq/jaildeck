package jails

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

// Changing an existing jail's limits (#119). A create writes them into the
// stanza's hooks and nothing touches them again (JD-018, JD-030); this is the
// one rewrite of a jail's configuration Jail Deck makes, and it is bounded to
// the hooks it wrote itself.
//
// Measured on isengard, 15.1-RELEASE-p5 (capture-limits-induced.sh):
//
//   - `rctl -a` for a resource that already has a deny rule replaces it, so a
//     changed value has no moment without a limit. A rule with another action
//     (an operator's `log`) is a separate rule and is left alone.
//   - `rctl -r jail:<name>:<resource>:deny` removes that one rule and keeps
//     the rest, the `log` one included.
//   - Every limit takes effect on a running jail at once; none waits for a
//     restart. A pcpu deny throttles a busy process within seconds, and a
//     cpuset moves the jail's threads already running elsewhere.
//   - A lower maxproc keeps the processes already there and refuses the next
//     fork. A lower memoryuse stops nothing and does not refuse the next
//     allocation at once.
//   - Re-adding a rule that is already there changes nothing, so the stanza's
//     exec.poststart applying it again at the next start does not double it.
const (
	OperationAddRule       = "add-rctl-rule"
	OperationRemoveRule    = "remove-rctl-rule"
	OperationSetCPUSet     = "set-cpuset"
	OperationRewriteConfig = "rewrite-jail-conf"
)

// LimitChanger changes a running jail's limits in the kernel. Each call
// returns the command it ran, for the audit log.
type LimitChanger interface {
	// AddRule is `rctl -a <rule>`, which replaces a rule for the same
	// subject, resource and action.
	AddRule(ctx context.Context, rule string) (string, error)
	// RemoveRule is `rctl -r <filter>`. A filter that matches nothing is not
	// an error here.
	RemoveRule(ctx context.Context, filter string) (string, error)
	// SetCPUSet is `cpuset -l <list> -j <name>`; an empty list unpins the
	// jail with `-l all`.
	SetCPUSet(ctx context.Context, name, list string) (string, error)
	// RuleUsage is `rctl -u jail:<name>`, each resource to its amount.
	RuleUsage(ctx context.Context, name string) (map[string]int64, error)
}

// ConfigReplacer rewrites a jail's own stanza in jail.conf.d.
type ConfigReplacer interface {
	// ReplaceConfig writes content in place of the file only while the file
	// still says old, and refuses with 409 otherwise: a configuration changed
	// since Jail Deck read it is somebody else's change, and replacing it
	// would lose that.
	ReplaceConfig(ctx context.Context, name, old, content string) (string, error)
}

// LimitsPatch is the body of PATCH /api/jails/{name}/limits: only the limits
// it names change, and null removes one.
type LimitsPatch map[string]json.RawMessage

// LimitsChange is what a PATCH did, read back from the host (JD-011).
type LimitsChange struct {
	Name string `json:"name"`
	// Limits are what the jail's stanza applies now, as it writes them.
	Limits  Limits `json:"limits"`
	Running bool   `json:"running"`
	// Changed is false for a request that asked for what the jail already
	// had, which is how a repeat of the same PATCH answers.
	Changed bool   `json:"changed"`
	Config  string `json:"config"`
	// Rules are what the kernel holds for the jail's name afterwards,
	// `rctl jail:<name>`, in the kernel's own units.
	Rules []string `json:"rules"`
	// Warnings are things that are true of the jail after the change and
	// that the limits alone do not say, such as a limit already exceeded.
	Warnings []string `json:"warnings,omitempty"`
}

// limitFields are the names a patch may carry: Limits' own JSON names.
var limitFields = []string{"memoryuse", "vmemoryuse", "maxproc", "openfiles", "pcpu", "cpuset"}

// rctlResources are the limits that are rctl rules, in the order Rules
// writes them.
var rctlResources = []string{"memoryuse", "vmemoryuse", "maxproc", "openfiles", "pcpu"}

// apply returns current with the patch laid over it, validated as a create
// validates limits.
//
// A zero or an empty value is refused rather than read as "no limit". In a
// spec, leaving a limit out is how no limit is asked for, and 0 is the same
// thing; in a patch, leaving it out means "unchanged", and a client sending 0
// to mean "remove" and one sending it by mistake look the same. null is
// unambiguous.
func (p LimitsPatch) apply(current Limits) (Limits, error) {
	if len(p) == 0 {
		return Limits{}, badSpec("the body names no limit to change. Send one or more of " +
			strings.Join(limitFields, ", ") + "; null removes one")
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return Limits{}, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Limits{}, err
	}
	for _, key := range slices.Sorted(maps.Keys(p)) {
		if !slices.Contains(limitFields, key) {
			return Limits{}, badSpec(fmt.Sprintf("%q is not a limit; a jail's limits are %s",
				key, strings.Join(limitFields, ", ")))
		}
		value := bytes.TrimSpace(p[key])
		switch string(value) {
		case "null":
			delete(fields, key)
			continue
		case "0", `""`:
			return Limits{}, badSpec(fmt.Sprintf(
				"limits.%s is %s; to remove the limit, send null", key, value))
		}
		fields[key] = value
	}
	merged, err := json.Marshal(fields)
	if err != nil {
		return Limits{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(merged))
	decoder.DisallowUnknownFields()
	var next Limits
	if err := decoder.Decode(&next); err != nil {
		return Limits{}, badSpec("could not read the limits: " + err.Error())
	}
	next, err = next.normalize()
	if err != nil {
		return Limits{}, err
	}
	// A string of spaces trims to nothing, which would remove the limit
	// without saying null.
	for key, value := range p {
		if string(bytes.TrimSpace(value)) != "null" && next.amount(key) == "" {
			return Limits{}, badSpec(fmt.Sprintf(
				"limits.%s is empty; to remove the limit, send null", key))
		}
	}
	return next, nil
}

// amount is one limit as the stanza writes it, empty when there is none.
func (l Limits) amount(resource string) string {
	switch resource {
	case "memoryuse":
		return l.MemoryUse
	case "vmemoryuse":
		return l.VMemoryUse
	case "maxproc":
		return positive(l.MaxProc)
	case "openfiles":
		return positive(l.OpenFiles)
	case "pcpu":
		return positive(l.PCPU)
	case "cpuset":
		return l.CPUSet
	}
	return ""
}

// rule is the one rctl rule for a resource, empty when there is none.
func (l Limits) rule(name, resource string) string {
	if amount := l.amount(resource); amount != "" {
		return fmt.Sprintf("jail:%s:%s:deny=%s", name, resource, amount)
	}
	return ""
}

// --- reading the limits back from the stanza ----------------------------

// limitsIn reads the limits a stanza's hooks apply, which is the only record
// of them there is: Jail Deck keeps none of its own (JD-011).
//
// A hook that is not one Jail Deck writes is refused rather than kept or
// dropped. The rewrite replaces every exec.poststart and exec.prestop line,
// so a command an operator added there would be lost, and keeping it would
// mean guessing where in the order it belongs.
func limitsIn(content, name string) (Limits, error) {
	hooks := hookPatterns(name)
	settings := settingsIn(content, name)
	var limits Limits
	seen := map[string]bool{}
	for _, command := range settings["exec.poststart"] {
		if match := hooks.rule.FindStringSubmatch(command); match != nil && !seen[match[1]] {
			seen[match[1]] = true
			if err := limits.set(match[1], match[2]); err != nil {
				return Limits{}, foreignHook(name, "exec.poststart", command)
			}
			continue
		}
		if match := hooks.cpuset.FindStringSubmatch(command); match != nil && !seen["cpuset"] {
			seen["cpuset"] = true
			limits.CPUSet = match[1]
			continue
		}
		return Limits{}, foreignHook(name, "exec.poststart", command)
	}
	for _, command := range settings["exec.prestop"] {
		if command != RemoveRulesCommand(name) {
			return Limits{}, foreignHook(name, "exec.prestop", command)
		}
	}
	return limits, nil
}

type limitHooks struct{ rule, cpuset *regexp.Regexp }

func hookPatterns(name string) limitHooks {
	quoted := regexp.QuoteMeta(name)
	return limitHooks{
		rule: regexp.MustCompile(`^rctl -a jail:` + quoted + `:(` +
			strings.Join(rctlResources, "|") + `):deny=(\S+)$`),
		cpuset: regexp.MustCompile(`^cpuset -l (\S+) -j ` + quoted + `$`),
	}
}

func (l *Limits) set(resource, amount string) error {
	switch resource {
	case "memoryuse":
		l.MemoryUse = amount
		return nil
	case "vmemoryuse":
		l.VMemoryUse = amount
		return nil
	}
	n, err := strconv.Atoi(amount)
	if err != nil || n <= 0 {
		return fmt.Errorf("%s=%s is not a count", resource, amount)
	}
	switch resource {
	case "maxproc":
		l.MaxProc = n
	case "openfiles":
		l.OpenFiles = n
	case "pcpu":
		l.PCPU = n
	}
	return nil
}

func foreignHook(name, setting, command string) error {
	return common.NewAppError(http.StatusConflict, fmt.Sprintf(
		"%s's stanza has %s = %q, which is not a limit Jail Deck writes. Jail Deck rewrites "+
			"every %s line when it changes limits, so it would lose that command; change this "+
			"jail's limits in its stanza by hand, or take the command out first", name, setting, command, setting))
}

// --- rewriting the stanza -----------------------------------------------

// headerBefore is the paragraph a stanza written before #119 opens with. A
// rewrite replaces it with confHeader, because the promise it makes is no
// longer the whole truth.
const headerBefore = `# Jail Deck does not rewrite this file afterwards, so anything changed here is
# kept. It is read back on every listing: a jail is what the host says it is,
# not what Jail Deck remembers (JD-011).`

// hookLineRE is an exec.poststart or exec.prestop line, the lines the limits
// live on and the only ones a rewrite touches.
var hookLineRE = regexp.MustCompile(`^\s*exec\.(poststart|prestop)\s*\+?=`)

var poststopLineRE = regexp.MustCompile(`^\s*exec\.poststop\s*\+?=`)

// rewriteHooks replaces the stanza's limit hooks with the ones limits needs,
// and leaves every other line as it is.
//
// The hooks go where they were, or where the generator puts them: before a
// VNET jail's exec.poststop, or at the end of the block. Each group keeps the
// blank line the generator opens it with, so rewriting a generated stanza
// gives exactly the file a create with the new limits would have written --
// which is what the comparison behind PUT holds it to (JD-016).
func rewriteHooks(content string, limits Limits, name string) (string, error) {
	content = strings.Replace(content, headerBefore, confHeader, 1)
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	at := -1
	for _, line := range lines {
		if hookLineRE.MatchString(stripComment(line)) {
			if n := len(kept); n > 0 && strings.TrimSpace(kept[n-1]) == "" {
				kept = kept[:n-1]
			}
			if at < 0 {
				at = len(kept)
			}
			continue
		}
		kept = append(kept, line)
	}
	if at < 0 {
		at = hookAnchor(kept)
		if at < 0 {
			return "", common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s's stanza has no closing brace Jail Deck can find, so it cannot place the limits in it", name))
		}
	}

	poststart, prestop := limits.hooks(name)
	var block []string
	for _, group := range []struct {
		setting  string
		commands []string
	}{{"exec.poststart", poststart}, {"exec.prestop", prestop}} {
		if len(group.commands) == 0 {
			continue
		}
		block = append(block, "")
		for i, command := range group.commands {
			operator := "="
			if i > 0 {
				operator = "+="
			}
			block = append(block, fmt.Sprintf("\t%s %s \"%s\";", group.setting, operator, command))
		}
	}
	out := slices.Concat(kept[:at], block, kept[at:])
	return strings.Join(out, "\n"), nil
}

// hookAnchor is where hooks go in a stanza that has none: before the blank
// line that opens exec.poststop, or before the block's closing brace.
func hookAnchor(lines []string) int {
	for i, line := range lines {
		if poststopLineRE.MatchString(stripComment(line)) {
			if i > 0 && strings.TrimSpace(lines[i-1]) == "" {
				return i - 1
			}
			return i
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(stripComment(lines[i])) == "}" {
			return i
		}
	}
	return -1
}

// --- the change ---------------------------------------------------------

// SetLimits changes a jail's limits: in the kernel at once when it runs, and
// in its stanza so the next start applies the same ones. Both or neither: a
// change the kernel took and the stanza does not carry is lost at the next
// start, and one the stanza carries and the kernel did not take is a jail
// running under limits its configuration does not describe.
//
// The kernel goes first, because it is the half that can refuse -- a cpu the
// host does not have, a limit rctl will not take -- and the stanza is then
// written only for a change that is already in effect. Each kernel step is
// undone in reverse if a later one, or the stanza, fails.
func (s *JailService) SetLimits(ctx context.Context, name JailName, patch LimitsPatch) (LimitsChange, error) {
	if s.configReader == nil || s.configReplacer == nil || s.limits == nil || s.rctl == nil {
		return LimitsChange{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck cannot rewrite a jail's configuration, so it cannot change its limits")
	}
	jail, err := s.Get(ctx, name)
	if err != nil {
		return LimitsChange{}, err
	}
	result := LimitsChange{Name: jail.Name, Running: jail.Status == JailStatusRunning}

	config, exists, err := s.configReader.ConfigFile(ctx, jail.Name)
	if err != nil {
		return result, err
	}
	result.Config = config
	if !exists {
		return result, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is not there, so %s is configured somewhere Jail Deck does not write -- most likely "+
				"/etc/jail.conf, which belongs to you. Change its limits there", config, jail.Name))
	}
	content, err := s.configReader.ReadConfig(ctx, jail.Name)
	if err != nil {
		return result, err
	}
	current, err := limitsIn(content, jail.Name)
	if err != nil {
		return result, err
	}
	result.Limits = current
	next, err := patch.apply(current)
	if err != nil {
		return result, err
	}

	if next == current {
		result.Rules, err = s.rctl.JailRules(ctx, jail.Name)
		return result, err
	}
	if next.rctl() {
		enabled, err := s.rctl.RacctEnabled(ctx)
		if err != nil {
			return result, common.NewAppError(http.StatusInternalServerError,
				"could not read whether this host accounts resource usage: "+err.Error())
		}
		if !enabled {
			return result, common.NewAppError(http.StatusBadRequest,
				"this host has kern.racct.enable=0, so rctl limits cannot be applied. Put "+
					"kern.racct.enable=1 in /boot/loader.conf and reboot, or change only limits.cpuset, "+
					"which needs no accounting (JD-018)")
		}
	}
	rewritten, err := rewriteHooks(content, next, jail.Name)
	if err != nil {
		return result, err
	}

	// From here the host changes, so a client going away must not leave it
	// half done.
	ctx = context.WithoutCancel(ctx)
	var undo []limitStep
	if result.Running {
		result.Warnings = s.overLimit(ctx, jail.Name, current, next)
		undo, err = s.applyLimits(ctx, jail.Name, current, next)
		if err != nil {
			return result, s.takeBack(ctx, jail.Name, undo, err)
		}
	} else if leftover, err := s.rctl.JailRules(ctx, jail.Name); err == nil && len(leftover) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%s is stopped, yet %d rctl rule(s) are still keyed to its name: %s. The next start "+
				"replaces the ones its stanza sets and keeps the rest; clear them with: %s",
			jail.Name, len(leftover), strings.Join(leftover, ", "), RemoveRulesCommand(jail.Name)))
	}

	path, err := s.configReplacer.ReplaceConfig(ctx, jail.Name, content, rewritten)
	s.logStep(ctx, OperationRewriteConfig, jail.Name, "rewrite "+path, err)
	if err != nil {
		return result, s.takeBack(ctx, jail.Name, undo, err)
	}
	result.Changed = true

	after, err := s.configReader.ReadConfig(ctx, jail.Name)
	if err != nil {
		return result, err
	}
	if result.Limits, err = limitsIn(after, jail.Name); err != nil {
		return result, err
	}
	result.Rules, err = s.rctl.JailRules(ctx, jail.Name)
	return result, err
}

// limitStep is one kernel change, kept so it can be undone.
type limitStep struct {
	operation string
	run       func() (string, error)
}

// applyLimits makes the kernel's rules and cpuset match next, one audited
// step per limit that changes, and returns the steps that undo what it did.
func (s *JailService) applyLimits(ctx context.Context, name string, current, next Limits) ([]limitStep, error) {
	var undo []limitStep
	step := func(do, back limitStep) error {
		command, err := do.run()
		s.logStep(ctx, do.operation, name, command, err)
		if err != nil {
			return hostRefused(command, err)
		}
		undo = append(undo, back)
		return nil
	}
	for _, resource := range rctlResources {
		was, want := current.rule(name, resource), next.rule(name, resource)
		if was == want {
			continue
		}
		if err := step(s.ruleStep(ctx, name, resource, want), s.ruleStep(ctx, name, resource, was)); err != nil {
			return undo, err
		}
	}
	if current.CPUSet != next.CPUSet {
		if err := step(s.cpusetStep(ctx, name, next.CPUSet), s.cpusetStep(ctx, name, current.CPUSet)); err != nil {
			return undo, err
		}
	}
	return undo, nil
}

// ruleStep puts a resource's rule in place, or removes it when rule is
// empty -- only the deny rule, so an operator's log rule stays.
func (s *JailService) ruleStep(ctx context.Context, name, resource, rule string) limitStep {
	if rule != "" {
		return limitStep{OperationAddRule, func() (string, error) { return s.limits.AddRule(ctx, rule) }}
	}
	filter := fmt.Sprintf("jail:%s:%s:deny", name, resource)
	return limitStep{OperationRemoveRule, func() (string, error) { return s.limits.RemoveRule(ctx, filter) }}
}

func (s *JailService) cpusetStep(ctx context.Context, name, list string) limitStep {
	return limitStep{OperationSetCPUSet, func() (string, error) { return s.limits.SetCPUSet(ctx, name, list) }}
}

// hostRefused says which command failed and in the host's own words, the
// way a refused start does (toAppError), rather than as a bare 500.
func hostRefused(command string, err error) error {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return common.NewAppError(http.StatusGatewayTimeout, fmt.Sprintf(
			"%s timed out and may not have completed", command))
	}
	message := err.Error()
	var cmdErr *system.CommandError
	if errors.As(err, &cmdErr) {
		message = cmdErr.Summary()
	}
	return common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
		"the host refused %s: %s", command, message))
}

// takeBack undoes the kernel steps already taken, newest first, and returns
// the error that made it necessary -- with what is still wrong when an undo
// failed too, because then the jail runs under limits its stanza does not
// carry.
func (s *JailService) takeBack(ctx context.Context, name string, undo []limitStep, cause error) error {
	var failed []string
	for i := len(undo) - 1; i >= 0; i-- {
		command, err := undo[i].run()
		s.logStep(ctx, undo[i].operation, name, command, err)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (%s)", command, err))
		}
	}
	if len(failed) == 0 {
		if len(undo) == 0 {
			return cause
		}
		return common.NewAppError(statusOf(cause), fmt.Sprintf(
			"%s. The limits changed before it were put back, so %s runs as it did", messageOf(cause), name))
	}
	common.Logger(ctx, "jail").Error("could not take back a limits change",
		slog.String("jail", name), slog.Any("failed", failed))
	return common.NewAppError(statusOf(cause), fmt.Sprintf(
		"%s. Taking back what had already changed failed too, so %s runs under limits its stanza "+
			"does not carry until it restarts: %s. `rctl jail:%s` shows what the kernel holds",
		messageOf(cause), name, strings.Join(failed, "; "), name))
}

// messageOf is an error's text without AppError's own framing.
func messageOf(err error) string {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return err.Error()
}

// overLimit names the limits the jail is already over once they are lowered,
// with what measuring found that means for it.
func (s *JailService) overLimit(ctx context.Context, name string, current, next Limits) []string {
	usage, err := s.limits.RuleUsage(ctx, name)
	if err != nil {
		common.Logger(ctx, "jail").Warn("could not read a jail's usage to compare with its new limits",
			slog.String("jail", name), slog.Any("err", err))
		return nil
	}
	var warnings []string
	for _, resource := range []string{"memoryuse", "vmemoryuse", "maxproc", "openfiles"} {
		amount := next.amount(resource)
		if amount == "" || amount == current.amount(resource) {
			continue
		}
		limit, ok := amountBytes(amount)
		used, known := usage[resource]
		if !ok || !known || used <= limit {
			continue
		}
		switch resource {
		case "memoryuse":
			warnings = append(warnings, fmt.Sprintf(
				"%s uses %s of resident memory, more than the new memoryuse of %s. Measured, a lower "+
					"memoryuse stops none of a jail's processes and does not refuse its next allocation at once",
				name, humanBytes(used), amount))
		case "vmemoryuse":
			warnings = append(warnings, fmt.Sprintf(
				"%s uses %s of address space, more than the new vmemoryuse of %s", name, humanBytes(used), amount))
		case "maxproc":
			warnings = append(warnings, fmt.Sprintf(
				"%s runs %d processes, more than the new maxproc of %s. Measured, they keep running, and "+
					"the jail cannot start another until it runs fewer than %s", name, used, amount, amount))
		case "openfiles":
			warnings = append(warnings, fmt.Sprintf(
				"%s has %d files open, more than the new openfiles of %s", name, used, amount))
		}
	}
	return warnings
}

// amountBytes reads a limit the way rctl does: a count, or bytes with a K, M,
// G or T suffix in powers of 1024 -- measured, deny=1G is 1073741824.
func amountBytes(amount string) (int64, bool) {
	if amount == "" {
		return 0, false
	}
	multiplier := int64(1)
	switch strings.ToUpper(amount[len(amount)-1:]) {
	case "K":
		multiplier = 1 << 10
	case "M":
		multiplier = 1 << 20
	case "G":
		multiplier = 1 << 30
	case "T":
		multiplier = 1 << 40
	}
	if multiplier > 1 {
		amount = amount[:len(amount)-1]
	}
	n, err := strconv.ParseInt(amount, 10, 64)
	if err != nil {
		return 0, false
	}
	return n * multiplier, true
}

func humanBytes(n int64) string {
	for _, unit := range []struct {
		suffix string
		size   int64
	}{{"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}} {
		if n >= unit.size {
			return strconv.FormatFloat(float64(n)/float64(unit.size), 'f', 1, 64) + unit.suffix
		}
	}
	return strconv.FormatInt(n, 10)
}
