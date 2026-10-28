package jails

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// OperationRemoveConfig is the audit operation for deleting a jail's file in
// jail.conf.d. The dataset's own destroy is audited by the storage domain,
// which is the one that runs zfs.
const OperationRemoveConfig = "remove-jail-conf"

// The audit operations for the two sweeps: an epair or rctl rules left behind
// by a start that failed, removed by a later create or destroy. Logged only
// when something was removed, or would not go -- a sweep that finds nothing
// changed nothing, and the audit log records what changed the host.
const (
	OperationRemoveLeftoverEpair = "remove-leftover-epair"
	OperationRemoveLeftoverRules = "remove-leftover-rctl"
)

// epairCommand is what removing a jail's leftover epair runs, for the audit
// entry: the interface is found by its description, so before it is found it
// can only be named by that.
func epairCommand(name, removed string) string {
	if removed == "" {
		return "ifconfig <interface described jail:" + name + "> destroy"
	}
	return "ifconfig " + removed + " destroy"
}

// The steps of a destroy, in the order they run. StepAutostart is shared with
// a create, which sets the same list the other way.
const (
	StepStop        = "stop"
	StepUnconfigure = "unconfigure"
	StepDestroy     = "destroy"
)

// Destroyed is what destroying a jail did, or got as far as doing. Like
// Created, it is returned filled in on failure too: nothing is rolled back,
// so what happened is the recovery information (JD-030).
type Destroyed struct {
	Jail string `json:"jail"`
	// Root is the jail's own dataset, named even when the destroy was
	// refused -- it is the dataset an operator would go and look at.
	Root string `json:"root,omitempty"`
	// Taken are the child datasets destroyed along with the root. A jail's
	// children go with it (JD-007), and they go silently unless listed here.
	Taken []string `json:"taken,omitempty"`
	// Stopped says the jail was running and this stopped it.
	Stopped bool `json:"stopped"`
	// Unlisted says it was taken out of rc.conf's jail_list.
	Unlisted bool `json:"unlisted"`
	// Config is the configuration file that was deleted.
	Config    string `json:"config,omitempty"`
	Destroyed bool   `json:"destroyed"`
	// Epair is the interface destroyed with the jail. Normally there is
	// none: a VNET jail's exec.poststop takes its own epair away when it
	// stops. One turns up here when an earlier start failed and left the
	// epair behind, which would otherwise outlive the jail (JD-030).
	Epair string `json:"epair,omitempty"`
	// EpairError says why a leftover epair could not be removed. The destroy
	// carried on regardless -- the jail and its dataset are the point, and a
	// stranded interface is a nuisance rather than a blocker. It is named
	// here because a silent one is an interface on a bridge that nothing
	// will ever account for.
	EpairError string `json:"epair_error,omitempty"`
	// Rules are the rctl rules cleared with the jail. Like the epair there
	// are normally none: a jail stopped cleanly has had them removed by its
	// own exec.prestop. One turns up here when an earlier start failed
	// (JD-018).
	Rules []string `json:"rules,omitempty"`
	// RulesError says why they could not be cleared, on the same terms as
	// EpairError -- except that a leftover rule is worse than a stranded
	// interface, because the next jail of this name would inherit it.
	RulesError string `json:"rules_error,omitempty"`
	// Blocked is set when something kept on purpose is in the way. Nothing
	// ran: the jail is still there, still configured, still as it was.
	Blocked *DestroyBlock `json:"blocked,omitempty"`
	// FailedAt names the step that stopped, and Error says why.
	FailedAt string `json:"failed_at,omitempty"`
	Error    string `json:"error,omitempty"`
}

// DestroyBlock names what is in the way, because "it is in use" without
// saying by what is a message an operator cannot act on.
type DestroyBlock struct {
	Reason    string   `json:"reason"`
	Snapshots []string `json:"snapshots,omitempty"`
	Clones    []string `json:"clones,omitempty"`
	Holds     []string `json:"holds,omitempty"`
}

// Destroy removes a jail: stop it, take it out of jail_list, delete its
// configuration file, destroy its dataset (JD-030).
//
// That order is the safety property, and the last step is the one that
// matters. Nothing here checks whether the jail is really down before calling
// zfs — `zfs destroy` refuses a dataset whose jail is still running, with
// `pool or dataset is busy`, and that refusal is kept rather than replaced by
// a check of Jail Deck's own. A check reads the state and then acts on what
// it read; the mount is held for as long as the jail is up. Only one of those
// can lose a race.
//
// **If a step fails, nothing is undone**, the same as a create. A jail that
// was stopped stays stopped and the result says which step it reached.
func (s *JailService) Destroy(ctx context.Context, name JailName, confirm string) (Destroyed, error) {
	if confirm != name.String() {
		return Destroyed{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"this destroys %s, its configuration and its filesystem; it needs confirm=%s", name, name))
	}
	jail, err := s.onZFS(ctx, name, s.rootDestroyer)
	if err != nil {
		return Destroyed{}, err
	}
	if s.configRemover == nil || s.autostart == nil {
		return Destroyed{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck is running without the configuration access it needs to destroy a jail")
	}
	result := Destroyed{Jail: name.String()}

	// Everything that can refuse, refuses before the jail is stopped. A jail
	// that cannot be destroyed must not be left shut down for nothing.
	dependents, err := s.rootDestroyer.CheckDestroy(ctx, jail.Path)
	if err != nil {
		return result, err
	}
	result.Root, result.Taken = dependents.Root, dependents.Children
	if dependents.Blocking() {
		result.Blocked = &DestroyBlock{
			Reason:    blockedReason(name, dependents),
			Snapshots: dependents.Snapshots,
			Clones:    dependents.Clones,
			Holds:     dependents.Holds,
		}
		return result, nil
	}

	// The jail has to be one Jail Deck can fully unconfigure. A stanza in
	// /etc/jail.conf belongs to the operator (JD-030) and nothing here will
	// edit it, so destroying the dataset would leave a jail still declared,
	// still listed, and pointing at a path that no longer exists.
	config, exists, err := s.configRemover.ConfigFile(ctx, name.String())
	if err != nil {
		return result, err
	}
	if !exists {
		return result, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is not there, so %s is configured somewhere Jail Deck does not write -- most likely "+
				"/etc/jail.conf, which belongs to you. Remove its block by hand and the dataset with "+
				"zfs destroy", config, name))
	}

	// Detached from the request like every other mutation: a client hanging
	// up must not leave a jail half destroyed (JD-014).
	opCtx := context.WithoutCancel(ctx)

	if jail.Status == JailStatusRunning {
		if _, err := s.Stop(opCtx, name); err != nil {
			result.FailedAt = StepStop
			return result, err
		}
		result.Stopped = true
	}

	// A stopped VNET jail has already taken its own epair away through
	// exec.poststop, so this normally finds nothing. It finds something when
	// an earlier start failed: jail(8) skips poststop then, and the orphan
	// would otherwise outlive the jail it is named after.
	s.sweepEpair(opCtx, name, &result)
	s.sweepRules(opCtx, name, &result)

	// Idempotent in both directions, so a jail that was never in the list
	// costs one command and no special case (JD-030).
	command, err := s.autostart.SetAutostart(opCtx, name.String(), false)
	s.logStep(opCtx, OperationSetAutostart, name.String(), command, err)
	if err != nil {
		result.FailedAt = StepAutostart
		return result, err
	}
	result.Unlisted = true

	path, err := s.configRemover.RemoveConfig(opCtx, name.String())
	// A file removal, not a command: there is no shell line an operator could
	// paste to repeat it, so the audit log says what was done to what.
	s.logStep(opCtx, OperationRemoveConfig, name.String(), "remove "+config, err)
	if err != nil {
		result.FailedAt = StepUnconfigure
		return result, err
	}
	result.Config = path

	root, err := s.rootDestroyer.DestroyRoot(opCtx, jail.Path)
	if err != nil {
		// The jail is gone from the host's configuration and its filesystem
		// is still there. Root is in the result for exactly this moment: it
		// is now the only name the dataset answers to.
		result.FailedAt = StepDestroy
		return result, err
	}
	result.Root = root
	result.Destroyed = true
	return result, nil
}

// sweepEpair destroys a leftover epair, and never fails the destroy. It is
// audited when it removed one or failed to.
//
// The jail and its dataset are what the caller asked to be rid of. An
// interface that cannot be removed is reported in the result and logged,
// not raised: refusing to finish would leave the jail half destroyed over a
// nuisance, which is the worse of the two outcomes.
func (s *JailService) sweepEpair(ctx context.Context, name JailName, result *Destroyed) {
	if s.epair == nil {
		return
	}
	removed, err := s.epair.RemoveJailEpair(ctx, name.String())
	if err != nil || removed != "" {
		s.logStep(ctx, OperationRemoveLeftoverEpair, name.String(), epairCommand(name.String(), removed), err)
	}
	if err != nil {
		result.EpairError = err.Error()
		common.Logger(ctx, "jail").Warn("could not remove the jail's leftover epair",
			slog.String("jail", name.String()),
			slog.Any("err", err))
		return
	}
	result.Epair = removed
}

// sweepRules clears whatever rctl rules are still keyed to the jail's name,
// and never fails the destroy, for the same reason sweepEpair does not.
//
// A jail stopped cleanly has already had them removed by its own
// exec.prestop, so this normally finds nothing. It finds something after a
// start that failed part way through exec.poststart, or for a jail somebody
// configured by hand. Clearing them matters more than the epair does: rules
// are keyed to the name and outlive the jail, so the next jail called this
// would quietly run under them (JD-018).
//
// Like sweepEpair, it is audited when it removed something or failed to, and
// reported in the result either way.
func (s *JailService) sweepRules(ctx context.Context, name JailName, result *Destroyed) {
	if s.rctl == nil {
		return
	}
	rules, err := s.rctl.JailRules(ctx, name.String())
	if err == nil {
		if len(rules) == 0 {
			return
		}
		var command string
		command, err = s.rctl.RemoveJailRules(ctx, name.String())
		if command == "" {
			command = RemoveRulesCommand(name.String())
		}
		s.logStep(ctx, OperationRemoveLeftoverRules, name.String(), command, err)
	}
	if err != nil {
		result.RulesError = err.Error()
		common.Logger(ctx, "jail").Warn("could not clear the jail's leftover rctl rules",
			slog.String("jail", name.String()),
			slog.Any("err", err))
		return
	}
	result.Rules = rules
}

// blockedReason says what is in the way and what to do about it, in the shape
// the template destroy already uses.
func blockedReason(name JailName, dependents RootDependents) string {
	parts := make([]string, 0, 3)
	if n := len(dependents.Snapshots); n > 0 {
		parts = append(parts, fmt.Sprintf("%d snapshot(s) are of it or of its children", n))
	}
	if n := len(dependents.Clones); n > 0 {
		parts = append(parts, fmt.Sprintf("%d dataset(s) are cloned from those snapshots", n))
	}
	if n := len(dependents.Holds); n > 0 {
		parts = append(parts, fmt.Sprintf("%d hold(s) are on them", n))
	}
	return "refused: " + strings.Join(parts, ", ") +
		". Each was kept on purpose, so each has to go first -- a snapshot with DELETE " +
		"/api/jails/" + name.String() + "/snapshots/{snapshot}, a clone by destroying it or by " +
		"zfs promote, which moves the dependency the other way."
}
