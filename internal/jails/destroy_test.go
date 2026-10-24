package jails

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

// --- doubles -----------------------------------------------------------

// stubDestroyer answers what the test set up and records what it was asked.
type stubDestroyer struct {
	dependents RootDependents
	checkErr   error
	destroyErr error

	checked   []string
	destroyed []string
	steps     *[]string
}

func (s *stubDestroyer) CheckDestroy(_ context.Context, jailPath string) (RootDependents, error) {
	s.checked = append(s.checked, jailPath)
	return s.dependents, s.checkErr
}

func (s *stubDestroyer) DestroyRoot(_ context.Context, jailPath string) (string, error) {
	s.destroyed = append(s.destroyed, jailPath)
	record(s.steps, "destroy-root")
	if s.destroyErr != nil {
		return "", s.destroyErr
	}
	return s.dependents.Root, nil
}

// stubRemover stands in for the file in jail.conf.d, and -- when it is given
// a system -- for what deleting that file does: the jail leaves the listing.
type stubRemover struct {
	path    string
	absent  bool
	statErr error
	err     error
	system  *createSystem

	asked   []string
	removed []string
	steps   *[]string
}

func (s *stubRemover) ConfigFile(_ context.Context, name string) (string, bool, error) {
	s.asked = append(s.asked, name)
	return s.path, !s.absent, s.statErr
}

func (s *stubRemover) RemoveConfig(_ context.Context, name string) (string, error) {
	s.removed = append(s.removed, name)
	record(s.steps, "remove-config")
	if s.err != nil {
		return "", s.err
	}
	if s.system != nil {
		s.system.existing = slices.DeleteFunc(s.system.existing,
			func(jail Jail) bool { return jail.Name == name })
	}
	return s.path, nil
}

var (
	_ RootDestroyer = (*stubDestroyer)(nil)
	_ ConfigRemover = (*stubRemover)(nil)
)

func destroyService(system *createSystem, destroyer *stubDestroyer, remover *stubRemover,
	autostart *stubAutostart, logger *fakeLogger) *JailService {
	return NewJailService(Dependencies{
		System: system, Audit: logger,
		RootDestroyer: destroyer, ConfigRemover: remover, Autostart: autostart,
	})
}

// destroyable is a running jail with a root, a configuration file and nothing
// kept on purpose beneath it -- the ordinary case every test below varies.
func destroyable() (*createSystem, *stubDestroyer, *stubRemover, *stubAutostart, *fakeLogger) {
	system := &createSystem{existing: []Jail{{
		Name: "db1", JID: "9", Status: JailStatusRunning,
		Path: "/usr/local/jails/containers/db1",
	}}}
	destroyer := &stubDestroyer{dependents: RootDependents{Root: "tank/jails/containers/db1"}}
	remover := &stubRemover{path: "/etc/jail.conf.d/db1.conf", system: system}
	return system, destroyer, remover, &stubAutostart{}, &fakeLogger{}
}

func appCode(t *testing.T, err error) int {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("want an *common.AppError, got %T: %v", err, err)
	}
	return appErr.Code
}

// --- refusals that touch nothing ---------------------------------------

func TestDestroyNeedsConfirm(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	service := destroyService(system, destroyer, remover, autostart, logger)

	for _, confirm := range []string{"", "DB1", "db2"} {
		_, err := service.Destroy(context.Background(), "db1", confirm)
		if got := appCode(t, err); got != http.StatusBadRequest {
			t.Fatalf("confirm=%q: want 400, got %d", confirm, got)
		}
		if !strings.Contains(err.Error(), "confirm=db1") {
			t.Errorf("confirm=%q: the message should say what to send, got %q", confirm, err)
		}
	}
	// Nothing was even looked at, let alone stopped.
	if len(destroyer.checked) != 0 || len(system.stopped) != 0 || len(remover.removed) != 0 {
		t.Errorf("a mistyped confirm touched the host: checked %v, stopped %v, removed %v",
			destroyer.checked, system.stopped, remover.removed)
	}
}

func TestDestroyUnknownJailIs404(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	service := destroyService(system, destroyer, remover, autostart, logger)

	_, err := service.Destroy(context.Background(), "nope", "nope")
	if got := appCode(t, err); got != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%v)", got, err)
	}
}

func TestDestroyRefusesAJailWithNoDatasetOfItsOwn(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	system.existing = []Jail{{Name: "db1", Status: JailStatusStopped}}
	service := destroyService(system, destroyer, remover, autostart, logger)

	_, err := service.Destroy(context.Background(), "db1", "db1")
	if !errors.Is(err, ErrJailNotOnZFS) {
		t.Fatalf("want ErrJailNotOnZFS, got %v", err)
	}
	if len(destroyer.checked) != 0 {
		t.Errorf("storage was asked about a jail that is not on ZFS: %v", destroyer.checked)
	}
}

func TestDestroyWithoutThePortsIs501(t *testing.T) {
	tests := []struct {
		name    string
		service func(*createSystem, *stubDestroyer, *stubRemover, *stubAutostart, *fakeLogger) *JailService
	}{
		{
			name: "no destroyer",
			service: func(s *createSystem, _ *stubDestroyer, r *stubRemover, a *stubAutostart, l *fakeLogger) *JailService {
				return NewJailService(Dependencies{System: s, Audit: l, ConfigRemover: r, Autostart: a})
			},
		},
		{
			name: "no config remover",
			service: func(s *createSystem, d *stubDestroyer, _ *stubRemover, a *stubAutostart, l *fakeLogger) *JailService {
				return NewJailService(Dependencies{System: s, Audit: l, RootDestroyer: d, Autostart: a})
			},
		},
		{
			name: "no autostart",
			service: func(s *createSystem, d *stubDestroyer, r *stubRemover, _ *stubAutostart, l *fakeLogger) *JailService {
				return NewJailService(Dependencies{System: s, Audit: l, RootDestroyer: d, ConfigRemover: r})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			system, destroyer, remover, autostart, logger := destroyable()
			_, err := test.service(system, destroyer, remover, autostart, logger).
				Destroy(context.Background(), "db1", "db1")
			if got := appCode(t, err); got != http.StatusNotImplemented {
				t.Fatalf("want 501, got %d (%v)", got, err)
			}
			if len(system.stopped) != 0 {
				t.Errorf("the jail was stopped before the 501: %v", system.stopped)
			}
		})
	}
}

// --- what blocks a destroy ---------------------------------------------

// Blocking is tested directly as well as through Destroy, because each field
// has to block on its own. In practice a hold is always on a snapshot and a
// clone always of one, so a test that sets them together would pass with the
// snapshot doing all the work and the other two clauses never running.
func TestRootDependentsBlocking(t *testing.T) {
	tests := []struct {
		name       string
		dependents RootDependents
		want       bool
	}{
		{name: "nothing", dependents: RootDependents{Root: "p/j"}},
		{
			name:       "children alone never block",
			dependents: RootDependents{Root: "p/j", Children: []string{"p/j/data"}},
		},
		{
			name:       "a snapshot",
			dependents: RootDependents{Root: "p/j", Snapshots: []string{"p/j@n"}},
			want:       true,
		},
		{
			name:       "a clone",
			dependents: RootDependents{Root: "p/j", Clones: []string{"p/t"}},
			want:       true,
		},
		{
			name:       "a hold",
			dependents: RootDependents{Root: "p/j", Holds: []string{"p/j@n (backup)"}},
			want:       true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.dependents.Blocking(); got != test.want {
				t.Errorf("Blocking() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDestroyIsBlockedByWhatWasKeptOnPurpose(t *testing.T) {
	tests := []struct {
		name       string
		dependents RootDependents
		inReason   string
	}{
		{
			name: "a snapshot",
			dependents: RootDependents{
				Root:      "tank/jails/containers/db1",
				Snapshots: []string{"tank/jails/containers/db1@nightly"},
			},
			inReason: "1 snapshot(s)",
		},
		{
			name: "a snapshot of a child",
			dependents: RootDependents{
				Root:      "tank/jails/containers/db1",
				Children:  []string{"tank/jails/containers/db1/data"},
				Snapshots: []string{"tank/jails/containers/db1/data@nightly"},
			},
			inReason: "1 snapshot(s)",
		},
		{
			name: "a template cloned from it",
			dependents: RootDependents{
				Root:      "tank/jails/containers/db1",
				Snapshots: []string{"tank/jails/containers/db1@base"},
				Clones:    []string{"tank/jails/templates/db"},
			},
			inReason: "1 dataset(s) are cloned",
		},
		{
			name: "a hold",
			dependents: RootDependents{
				Root:      "tank/jails/containers/db1",
				Snapshots: []string{"tank/jails/containers/db1@keep"},
				Holds:     []string{"tank/jails/containers/db1@keep (backup)"},
			},
			inReason: "1 hold(s)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			system, destroyer, remover, autostart, logger := destroyable()
			destroyer.dependents = test.dependents
			service := destroyService(system, destroyer, remover, autostart, logger)

			result, err := service.Destroy(context.Background(), "db1", "db1")
			// A refusal, not a failure: the handler turns it into a 409 and
			// the body says what is in the way.
			if err != nil {
				t.Fatalf("want a blocked result and no error, got %v", err)
			}
			if result.Blocked == nil {
				t.Fatal("want Blocked to be set")
			}
			if result.Destroyed {
				t.Error("Destroyed is true on a refusal")
			}
			if !strings.Contains(result.Blocked.Reason, test.inReason) {
				t.Errorf("reason %q should mention %q", result.Blocked.Reason, test.inReason)
			}
			if result.Root != test.dependents.Root {
				t.Errorf("Root = %q, want %q -- a refusal still has to name the dataset",
					result.Root, test.dependents.Root)
			}
			// Nothing ran. This is the whole point of asking first.
			if len(system.stopped) != 0 || autostart.call != 0 || len(remover.removed) != 0 ||
				len(destroyer.destroyed) != 0 {
				t.Errorf("a blocked destroy acted: stopped %v, autostart %d, removed %v, destroyed %v",
					system.stopped, autostart.call, remover.removed, destroyer.destroyed)
			}
		})
	}
}

// A jail's children are destroyed with it (JD-007), so they must not block --
// but they have to be named, because otherwise they go silently.
func TestDestroyTakesTheChildrenAndNamesThem(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	children := []string{"tank/jails/containers/db1/data", "tank/jails/containers/db1/logs"}
	destroyer.dependents.Children = children
	service := destroyService(system, destroyer, remover, autostart, logger)

	result, err := service.Destroy(context.Background(), "db1", "db1")
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if result.Blocked != nil {
		t.Fatalf("children blocked the destroy: %+v", result.Blocked)
	}
	if !slices.Equal(result.Taken, children) {
		t.Errorf("Taken = %v, want %v", result.Taken, children)
	}
}

func TestDestroyRefusesAJailItCannotUnconfigure(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	remover.absent = true
	service := destroyService(system, destroyer, remover, autostart, logger)

	result, err := service.Destroy(context.Background(), "db1", "db1")
	if got := appCode(t, err); got != http.StatusConflict {
		t.Fatalf("want 409, got %d (%v)", got, err)
	}
	// The operator has to be told where the block actually is, and that this
	// is not a bug they can fix by retrying.
	if !strings.Contains(err.Error(), "/etc/jail.conf") {
		t.Errorf("the message should name the file that belongs to the operator, got %q", err)
	}
	if result.Destroyed || len(system.stopped) != 0 || len(destroyer.destroyed) != 0 {
		t.Errorf("a jail Jail Deck cannot unconfigure was acted on: %+v, stopped %v", result, system.stopped)
	}
}

// --- the order, which is the safety property ---------------------------

func TestDestroyRunsTheStepsInOrder(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	var steps []string
	system.steps, destroyer.steps, remover.steps, autostart.steps = &steps, &steps, &steps, &steps
	service := destroyService(system, destroyer, remover, autostart, logger)

	result, err := service.Destroy(context.Background(), "db1", "db1")
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}

	if !slices.Equal(system.stopped, []string{"db1"}) {
		t.Errorf("stopped = %v, want the jail stopped exactly once", system.stopped)
	}
	if autostart.set["db1"] {
		t.Error("the jail was left in jail_list; a destroyed jail must not come back at boot")
	}
	if !slices.Equal(remover.removed, []string{"db1"}) {
		t.Errorf("removed = %v, want the configuration file deleted", remover.removed)
	}
	if !slices.Equal(destroyer.destroyed, []string{"/usr/local/jails/containers/db1"}) {
		t.Errorf("destroyed = %v, want the dataset destroyed by the jail's path", destroyer.destroyed)
	}

	// The order is the safety property, not an implementation detail. The
	// dataset goes last because every earlier step is what makes it
	// destroyable, and the jail leaves jail_list before its configuration
	// does so a crash in between cannot leave a name that boots into
	// nothing (JD-030).
	wantSteps := []string{"stop", "autostart", "remove-config", "destroy-root"}
	if !slices.Equal(steps, wantSteps) {
		t.Errorf("steps = %v, want %v", steps, wantSteps)
	}

	want := Destroyed{
		Jail: "db1", Root: "tank/jails/containers/db1",
		Stopped: true, Unlisted: true, Config: "/etc/jail.conf.d/db1.conf", Destroyed: true,
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("result = %+v, want %+v", result, want)
	}

	// The audit log carries the steps this domain owns. The dataset's destroy
	// is logged by the storage domain, which is the one that runs zfs.
	var operations []string
	for _, entry := range logger.entries {
		operations = append(operations, entry.Operation)
	}
	if !slices.Equal(operations, []string{"stop", OperationSetAutostart, OperationRemoveConfig}) {
		t.Errorf("audit operations = %v", operations)
	}
	for _, entry := range logger.entries {
		if !entry.Success {
			t.Errorf("entry %q reports a failure: %+v", entry.Operation, entry)
		}
	}
}

// A jail that is already down is not stopped again, and the rest still runs.
func TestDestroyDoesNotStopAJailThatIsAlreadyStopped(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	system.existing[0].Status = JailStatusStopped
	system.existing[0].JID = ""
	service := destroyService(system, destroyer, remover, autostart, logger)

	result, err := service.Destroy(context.Background(), "db1", "db1")
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if len(system.stopped) != 0 {
		t.Errorf("a stopped jail was stopped again: %v", system.stopped)
	}
	if result.Stopped {
		t.Error("Stopped is true for a jail that was already down")
	}
	if !result.Destroyed {
		t.Error("the destroy did not finish")
	}
}

// The jail_list entry goes even for a jail that was never in it: sysrc -= is
// idempotent, so asking unconditionally is cheaper than reading the list and
// cannot leave a destroyed name behind (JD-030).
func TestDestroyAlwaysTakesTheNameOutOfJailList(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	service := destroyService(system, destroyer, remover, autostart, logger)

	if _, err := service.Destroy(context.Background(), "db1", "db1"); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if autostart.call != 1 {
		t.Fatalf("SetAutostart called %d times, want 1", autostart.call)
	}
	if enabled, ok := autostart.set["db1"]; !ok || enabled {
		t.Errorf("SetAutostart(db1, %v); want it called with false", enabled)
	}
}

// --- a step that fails -------------------------------------------------

func TestDestroyReportsTheStepThatFailed(t *testing.T) {
	boom := common.NewAppError(http.StatusInternalServerError, "boom")

	tests := []struct {
		name     string
		arrange  func(*createSystem, *stubDestroyer, *stubRemover, *stubAutostart)
		failedAt string
		// what must still be true afterwards, because nothing is undone
		check func(*testing.T, Destroyed)
	}{
		{
			name: "the stop",
			arrange: func(s *createSystem, _ *stubDestroyer, _ *stubRemover, _ *stubAutostart) {
				s.stopErr = boom
			},
			failedAt: StepStop,
			check: func(t *testing.T, result Destroyed) {
				if result.Unlisted || result.Config != "" {
					t.Errorf("the destroy carried on past a failed stop: %+v", result)
				}
			},
		},
		{
			name: "the jail_list edit",
			arrange: func(_ *createSystem, _ *stubDestroyer, _ *stubRemover, a *stubAutostart) {
				a.err = boom
			},
			failedAt: StepAutostart,
			check: func(t *testing.T, result Destroyed) {
				if !result.Stopped {
					t.Error("the jail was stopped and the result does not say so")
				}
				if result.Config != "" {
					t.Error("the configuration file was removed after jail_list failed")
				}
			},
		},
		{
			name: "removing the configuration file",
			arrange: func(_ *createSystem, _ *stubDestroyer, r *stubRemover, _ *stubAutostart) {
				r.err = boom
			},
			failedAt: StepUnconfigure,
			check: func(t *testing.T, result Destroyed) {
				if !result.Stopped || !result.Unlisted {
					t.Errorf("the steps that did run are not reported: %+v", result)
				}
			},
		},
		{
			name: "the dataset",
			arrange: func(_ *createSystem, d *stubDestroyer, _ *stubRemover, _ *stubAutostart) {
				d.destroyErr = boom
			},
			failedAt: StepDestroy,
			check: func(t *testing.T, result Destroyed) {
				if result.Destroyed {
					t.Error("Destroyed is true after zfs destroy failed")
				}
				// The jail is gone from the configuration and the filesystem
				// is still there; this name is the only way back to it.
				if result.Root != "tank/jails/containers/db1" {
					t.Errorf("Root = %q, want the dataset left behind to be named", result.Root)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			system, destroyer, remover, autostart, logger := destroyable()
			test.arrange(system, destroyer, remover, autostart)
			service := destroyService(system, destroyer, remover, autostart, logger)

			result, err := service.Destroy(context.Background(), "db1", "db1")
			if err == nil {
				t.Fatal("want an error")
			}
			if result.FailedAt != test.failedAt {
				t.Errorf("FailedAt = %q, want %q", result.FailedAt, test.failedAt)
			}
			if result.Destroyed {
				t.Error("Destroyed is true after a failure")
			}
			test.check(t, result)
		})
	}
}

// A failing audit log must not stop the destroy: the log records what was
// done, and refusing to act because the record failed leaves the jail in the
// state the operator asked to leave.
func TestDestroySurvivesAnUnwritableAuditLog(t *testing.T) {
	system, destroyer, remover, autostart, _ := destroyable()
	service := NewJailService(Dependencies{
		System: system, Audit: erroringLogger{},
		RootDestroyer: destroyer, ConfigRemover: remover, Autostart: autostart,
	})

	result, err := service.Destroy(context.Background(), "db1", "db1")
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if !result.Destroyed {
		t.Error("the destroy did not finish")
	}
}
