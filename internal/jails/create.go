package jails

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

// Audit operations for the steps that are this domain's own. The clone is
// logged by the storage domain, which is the one that runs zfs.
const (
	OperationWriteConfig  = "write-jail-conf"
	OperationSetAutostart = "set-autostart"
)

// Create builds a jail from a ready template: clone the template's snapshot
// into the jail's own dataset, write its stanza, set autostart if it was
// asked for, start it, and confirm that it is running (JD-007, JD-030).
//
// The order is the safety property. Everything that can be refused is refused
// before anything is created, and the jail is started last, when there is
// something to start.
//
// **A step that fails stops the create and nothing is undone.** The result
// says which step, and what exists so far — a dataset that was cloned stays
// cloned, a stanza that was written stays written. An automatic rollback is a
// second destructive operation running at the moment least is known about the
// state, which is the position JD-026 already takes for a re-clone.
func (s *JailService) Create(ctx context.Context, spec Spec) (Created, error) {
	spec, err := spec.normalize()
	if err != nil {
		return Created{}, err
	}
	if s.rootCloner == nil || s.config == nil {
		return Created{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck is running without the storage and configuration it needs to create a jail")
	}
	if spec.Autostart && s.autostart == nil {
		return Created{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck cannot set autostart")
	}
	if spec.Network.Mode == NetworkVNET && (s.epairs == nil || s.epair == nil) {
		return Created{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck cannot build the epair a vnet jail needs")
	}

	result := Created{From: spec.Template}

	// Two refusals before anything exists: a name already in use, and a
	// template that cannot be cloned.
	switch _, err := s.Get(ctx, JailName(spec.Name)); {
	case err == nil:
		return result, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"a jail named %q already exists", spec.Name))
	case !errors.Is(err, ErrJailNotFound):
		// The host could not be read. Creating a jail without knowing whether
		// the name is taken is how two things end up called the same thing.
		result.FailedAt = StepCheck
		return result, err
	}
	if err := s.rootCloner.CheckTemplate(ctx, spec.Template); err != nil {
		result.FailedAt = StepCheck
		return result, err
	}
	if err := s.checkBridge(ctx, spec.Network); err != nil {
		result.FailedAt = StepCheck
		return result, err
	}
	if err := s.checkRctl(ctx, spec); err != nil {
		result.FailedAt = StepCheck
		return result, err
	}
	if err := s.checkDevfsRuleset(ctx, spec); err != nil {
		result.FailedAt = StepCheck
		return result, err
	}

	// Detached from the request like every other mutation: a client hanging
	// up must not leave a jail half built (JD-014).
	opCtx := context.WithoutCancel(ctx)

	// Reserved before the clone, because it is the last thing that can be
	// refused without leaving a dataset behind.
	at := Placement{}
	if spec.Network.Mode == NetworkVNET {
		at.Epair, err = s.epairs.FreeEpairUnit(ctx)
		if err != nil {
			result.FailedAt = StepCheck
			return result, err
		}
		result.Epair = at.Epair + "a"
	}

	root, err := s.rootCloner.CloneRoot(opCtx, spec.Template, spec.Name)
	if err != nil {
		result.FailedAt = StepClone
		return result, err
	}
	result.Root, result.Mountpoint = root.Dataset, root.Mountpoint

	at.Path = root.Mountpoint
	content, err := spec.Stanza(at)
	if err != nil {
		result.FailedAt = StepConfigure
		return result, err
	}
	path, writeErr := s.config.WriteConfig(opCtx, spec.Name, content)
	// Logged as a write of a file rather than as a command, because that is
	// what it is: there is no shell line an operator could paste to repeat it.
	s.logStep(opCtx, OperationWriteConfig, spec.Name, "write "+path, writeErr)
	if writeErr != nil {
		result.FailedAt = StepConfigure
		return result, writeErr
	}
	result.Config = path

	if spec.Autostart {
		command, err := s.autostart.SetAutostart(opCtx, spec.Name, true)
		s.logStep(opCtx, OperationSetAutostart, spec.Name, command, err)
		if err != nil {
			result.FailedAt = StepAutostart
			return result, err
		}
		result.Autostart = true
	}

	if !spec.starts() {
		// Read the jail back rather than describing the one just asked for.
		// The host is the record (JD-011), and this is the first moment it
		// can be asked about a jail that now exists.
		jail, err := s.Get(opCtx, JailName(spec.Name))
		if err != nil {
			result.FailedAt = StepStart
			return result, err
		}
		result.Jail = jail
		return result, nil
	}

	// Start goes through the adapter, which runs `service jail start` and then
	// confirms the jail is actually running. That confirmation is the whole
	// point: `service jail start` exits 0 whether the jail came up or not
	// (JD-030), so the exit code is not an answer.
	started, startErr := s.Start(opCtx, JailName(spec.Name))
	result.Jail = started.Jail
	if startErr != nil {
		result.FailedAt = StepStart
		s.cleanRules(opCtx, spec, &result)
		return result, s.cleanEpair(opCtx, spec, &result, startErr)
	}
	result.Running = true
	return result, nil
}

// checkBridge refuses a bridge the host does not have, before anything is
// created. A typo here would otherwise surface as a start that fails *after*
// the epair exists -- the one failure that leaves something behind.
func (s *JailService) checkBridge(ctx context.Context, network Network) error {
	if network.Mode != NetworkVNET || s.bridges == nil {
		return nil
	}
	bridges, err := s.bridges.Bridges(ctx)
	if err != nil {
		// Unreadable, not absent. Creating the jail anyway would risk the
		// leak this check exists to avoid.
		return common.NewAppError(http.StatusInternalServerError,
			"could not read the host's bridges: "+err.Error())
	}
	if slices.Contains(bridges, network.Bridge) {
		return nil
	}
	have := "the host has none"
	if len(bridges) > 0 {
		have = "the host has " + strings.Join(bridges, ", ")
	}
	return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
		"no bridge named %q; %s. Jail Deck never creates one (JD-017)", network.Bridge, have))
}

// checkRctl makes the two refusals the kernel's accounting calls for, before
// anything is created.
//
// A spec asking for rctl limits on a host that is not accounting is refused
// rather than built: the jail would not start anyway -- jail(8) destroys a
// jail whose exec.poststart fails -- and "it would not start" is a worse
// answer than "this host cannot do that, here is how to change it".
//
// A name whose rules are still in the kernel is refused for the opposite
// reason: that jail *would* start, and would silently run under limits
// nobody asked it for. Measured on 15.1-RELEASE-p5: rules are keyed to the
// name, outlive the jail, and can even be added for a name no jail answers
// to. Jail Deck will not clear somebody else's rules on its own -- it names
// them and the one command that does.
func (s *JailService) checkRctl(ctx context.Context, spec Spec) error {
	if s.rctl == nil {
		return nil
	}
	enabled, err := s.rctl.RacctEnabled(ctx)
	if err != nil {
		return common.NewAppError(http.StatusInternalServerError,
			"could not read whether this host accounts resource usage: "+err.Error())
	}
	if !enabled {
		if spec.Limits.rctl() {
			return common.NewAppError(http.StatusBadRequest,
				"this host has kern.racct.enable=0, so rctl limits cannot be applied and a jail asking "+
					"for them would not start. Put kern.racct.enable=1 in /boot/loader.conf and reboot, "+
					"or ask only for limits.cpuset, which needs no accounting (JD-018)")
		}
		// Nothing is accounted, so nothing can have been left behind either.
		return nil
	}
	rules, err := s.rctl.JailRules(ctx, spec.Name)
	if err != nil {
		return common.NewAppError(http.StatusInternalServerError,
			"could not read the rctl rules keyed to "+spec.Name+": "+err.Error())
	}
	if len(rules) == 0 {
		return nil
	}
	return common.NewAppError(http.StatusConflict, fmt.Sprintf(
		"no jail is named %q, but %d rctl rule(s) are still keyed to that name and a new jail would "+
			"run under them: %s. Clear them with: %s",
		spec.Name, len(rules), strings.Join(rules, ", "), RemoveRulesCommand(spec.Name)))
}

// checkDevfsRuleset refuses a ruleset number the kernel has not loaded.
//
// The one check here that is about confinement rather than accuracy.
// Measured on 15.1-RELEASE-p5: an unloaded number names an empty ruleset, an
// empty ruleset hides nothing, and the jail comes up with the host's whole
// /dev -- ada0, mem, kmem, io, bpf -- while jls reports the number that was
// asked for. A jail with no devfs_ruleset at all gets the restricted set, so
// this is strictly a failure of asking for one (JD-018).
func (s *JailService) checkDevfsRuleset(ctx context.Context, spec Spec) error {
	if s.devfsRulesets == nil {
		return nil
	}
	wanted, asked := asInt(spec.Params["devfs_ruleset"])
	if !asked || wanted == 0 {
		return nil
	}
	loaded, err := s.devfsRulesets.DevfsRulesetNumbers(ctx)
	if err != nil {
		return common.NewAppError(http.StatusInternalServerError,
			"could not read which devfs rulesets this kernel has loaded: "+err.Error())
	}
	if slices.Contains(loaded, wanted) {
		return nil
	}
	have := "it has loaded none"
	if len(loaded) > 0 {
		have = "it has loaded " + joinNumbers(loaded)
	}
	return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
		"this kernel has no devfs ruleset %d; %s. A jail given a ruleset the kernel does not have "+
			"gets the host's whole /dev instead of a restricted one, so it is refused rather than "+
			"created. Rulesets live in /etc/devfs.rules and are yours (JD-018)", wanted, have))
}

func joinNumbers(numbers []int) string {
	text := make([]string, 0, len(numbers))
	for _, n := range numbers {
		text = append(text, strconv.Itoa(n))
	}
	return strings.Join(text, ", ")
}

// cleanRules takes back the rctl rules a start that could not be confirmed
// had already applied.
//
// jail(8) destroys a jail whose exec.poststart fails and does not run
// exec.prestop on the way, so a poststart that got as far as its second rule
// leaves the first one in the kernel. Measured.
//
// Unlike the epair, a leftover rule does not block a retry on the host: the
// retry would add the same rules again. It blocks it *here*, because the
// next create refuses a name whose rules are still keyed to it. That is why
// a cleanup that fails is reported with the command that clears it, rather
// than being left to turn up later as a refusal with no history.
func (s *JailService) cleanRules(ctx context.Context, spec Spec, result *Created) {
	if s.rctl == nil || !spec.Limits.rctl() {
		return
	}
	rules, err := s.rctl.JailRules(ctx, spec.Name)
	if err == nil {
		if len(rules) == 0 {
			return
		}
		_, err = s.rctl.RemoveJailRules(ctx, spec.Name)
	}
	if err != nil {
		result.RulesError = fmt.Sprintf(
			"the rctl rules its start had already applied are still in the kernel, and the next "+
				"create of %q will refuse until they are gone; clear them with: %s (%s)",
			spec.Name, RemoveRulesCommand(spec.Name), err)
		common.Logger(ctx, "jail").Warn("could not take back the jail's rctl rules",
			slog.String("jail", spec.Name),
			slog.Any("err", err))
		return
	}
	result.RulesRemoved = rules
}

// cleanEpair destroys the epair a start that could not be confirmed left
// behind, and folds the outcome into the error the caller will return.
//
// The only thing a half-created jail undoes (JD-030), and only because it is
// the only leftover that *blocks the retry* rather than merely sitting there:
// jail(8) skips exec.poststop for a failed start, so nothing else ever
// destroys it, and the next attempt dies at `ifconfig ... already exists`.
//
// A cleanup that itself fails names the orphan in the error rather than
// swallowing it. A leaked epair somebody has been told about is a minute's
// work; a silent one is a jail that will never start again and no clue why.
func (s *JailService) cleanEpair(ctx context.Context, spec Spec, result *Created, startErr error) error {
	if spec.Network.Mode != NetworkVNET || s.epair == nil {
		return startErr
	}
	removed, err := s.epair.RemoveJailEpair(ctx, spec.Name)
	if err != nil {
		return common.NewAppError(statusOf(startErr), fmt.Sprintf(
			"%s. Its epair %s is still on %s and will block every retry; "+
				"destroy it with: ifconfig %s destroy (cleaning it up failed: %s)",
			startErr, result.Epair, spec.Network.Bridge, result.Epair, err))
	}
	result.EpairRemoved = removed != ""
	return startErr
}

// statusOf keeps the status the failed start earned, so adding the epair's
// fate to the message does not turn a refusal into a 500.
func statusOf(err error) int {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return http.StatusInternalServerError
}

// logStep writes one audit line for a step of a larger operation, in the same
// shape the template stages use: a create is several audited actions rather
// than one, and an operator reading the log should see each of them.
func (s *JailService) logStep(ctx context.Context, operation, target, command string, opErr error) {
	entry := audit.Entry{
		Timestamp: time.Now(),
		Operation: operation,
		Target:    target,
		Command:   command,
		Success:   opErr == nil,
	}
	if opErr != nil {
		entry.TimedOut = errors.Is(opErr, context.DeadlineExceeded)
		var cmdErr *system.CommandError
		if errors.As(opErr, &cmdErr) {
			entry.ExitCode = cmdErr.Result.ExitCode
			entry.Error = cmdErr.Unwrap().Error()
		} else {
			entry.Error = opErr.Error()
			entry.ExitCode = -1
		}
	}
	if err := s.operationLogger.Log(ctx, entry); err != nil {
		common.Logger(ctx, "jail").Error("failed to write operation log",
			slog.String("operation", operation),
			slog.String("target", target),
			slog.Any("err", err))
	}
}
