package storage

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const (
	recloneRoot = "tank/jails/containers/ottermq"
	oldTemplate = "tank/jails/templates/15.1-RELEASE@base"
	newTemplate = "tank/jails/templates/15.1-RELEASE@p5"
)

// A jail as the deployment target really has one: a root that is a clone of
// templates/15.1-RELEASE@base, a child dataset of its own, and a template that
// has since moved on to @p5.
func withJailToReclone() *stubSystem {
	return &stubSystem{
		pools: []string{"tank"},
		datasets: []Dataset{
			{Name: "tank/jails", Mountpoint: "/usr/local/jails"},
			{Name: "tank/jails/templates/15.1-RELEASE", Mountpoint: "/usr/local/jails/templates/15.1-RELEASE"},
			{Name: recloneRoot, Mountpoint: "/usr/local/jails/containers/ottermq"},
			{Name: recloneRoot + "/data", Mountpoint: "/usr/local/jails/containers/ottermq/data"},
			{Name: recloneRoot + "/logs", Mountpoint: "/usr/local/jails/containers/ottermq/logs"},
		},
		snapshots: []Snapshot{
			{Name: oldTemplate, Dataset: "tank/jails/templates/15.1-RELEASE", Short: "base", CreateTXG: 10},
			{Name: newTemplate, Dataset: "tank/jails/templates/15.1-RELEASE", Short: "p5", CreateTXG: 20},
		},
		origins:    map[string]string{recloneRoot: oldTemplate},
		clones:     map[string][]string{},
		notMounted: map[string]bool{},
	}
}

func TestRecloneMovesChildrenOutAndBack(t *testing.T) {
	system := withJailToReclone()
	logger := &fakeLogger{}

	result, err := NewService(system, logger, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err != nil {
		t.Fatalf("RecloneRoot() error = %v", err)
	}

	if result.From != oldTemplate || result.To != newTemplate {
		t.Errorf("from %q to %q, want %q to %q", result.From, result.To, oldTemplate, newTemplate)
	}
	// Named for the snapshot it came from, so two upgrades park under
	// different names and the parked copy says what it is.
	if result.OldRoot != "tank/jails/retained/ottermq-root-base" {
		t.Errorf("OldRoot = %q, want it named for the snapshot it came from", result.OldRoot)
	}
	want := []string{recloneRoot + "/data", recloneRoot + "/logs"}
	if !reflect.DeepEqual(result.Children, want) {
		t.Errorf("Children = %q, want %q", result.Children, want)
	}
	// Nothing is outstanding: the children are home and the old root is where
	// OldRoot says, which is where it is meant to be.
	if len(result.Parked) != 0 {
		t.Errorf("Parked = %+v, want nothing outstanding after a clean run", result.Parked)
	}
	if result.FailedAt != "" {
		t.Errorf("FailedAt = %q after a clean run", result.FailedAt)
	}
}

// The safety property, asserted directly rather than inferred: a re-clone runs
// no destroy of any kind. Everything it moves out of the way is renamed.
func TestRecloneDestroysNothing(t *testing.T) {
	for _, failAt := range []string{"", "clone", "restore"} {
		system := withJailToReclone()
		switch failAt {
		case "clone":
			system.cloneErr = errors.New("cannot clone: out of space")
		case "restore":
			system.mountErr = errors.New("cannot mount: busy")
			system.mountErrFor = map[string]bool{recloneRoot + "/data": true}
		}
		_, _ = NewService(system, &fakeLogger{}, "tank").
			RecloneRoot(context.Background(), recloneRoot, newTemplate)

		for _, command := range system.ran {
			if strings.Contains(command, "destroy") {
				t.Errorf("failAt=%q ran %q; a re-clone must never destroy anything", failAt, command)
			}
		}
	}
}

// Every child leaves the jail before anything touches the root. If that order
// were ever reversed, renaming the root would carry its children inside the
// parked copy, where nothing would look for them.
func TestRecloneMovesEveryChildBeforeTheRoot(t *testing.T) {
	system := withJailToReclone()
	if _, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate); err != nil {
		t.Fatalf("RecloneRoot() error = %v", err)
	}

	rootMoved, childrenMoved := -1, 0
	for i, command := range system.ran {
		if !strings.HasPrefix(command, "zfs rename") {
			continue
		}
		switch {
		case strings.Contains(command, "ottermq-root-"):
			rootMoved = i
		case strings.Contains(command, "retained/ottermq-"):
			if rootMoved >= 0 {
				continue // the restores, which come after
			}
			childrenMoved++
		}
	}
	if rootMoved < 0 {
		t.Fatalf("the root was never moved: %q", system.ran)
	}
	if childrenMoved != 2 {
		t.Errorf("%d children moved before the root, want 2: %q", childrenMoved, system.ran)
	}
}

func TestRecloneRefusesBeforeMovingAnything(t *testing.T) {
	tests := []struct {
		name     string
		template string
		wantCode int
	}{
		{name: "no such snapshot", template: "tank/jails/templates/15.1-RELEASE@p9", wantCode: http.StatusNotFound},
		{name: "no such template", template: "tank/jails/templates/14.2-RELEASE@base", wantCode: http.StatusNotFound},
		{name: "not a snapshot name", template: "tank/jails/templates/15.1-RELEASE", wantCode: http.StatusBadRequest},
		{name: "empty", template: "", wantCode: http.StatusBadRequest},
		{name: "a snapshot of the jail itself", template: recloneRoot + "@yesterday", wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withJailToReclone()
			result, err := NewService(system, &fakeLogger{}, "tank").
				RecloneRoot(context.Background(), recloneRoot, tt.template)
			if err == nil {
				t.Fatalf("RecloneRoot(%q) returned no error", tt.template)
			}
			if code := appErrCode(t, err); code != tt.wantCode {
				t.Errorf("error code = %d, want %d (%v)", code, tt.wantCode, err)
			}
			if len(system.ran) != 0 {
				t.Errorf("ran %q for a refused re-clone, want nothing", system.ran)
			}
			if result.FailedAt != StepReadState {
				t.Errorf("FailedAt = %q, want %q", result.FailedAt, StepReadState)
			}
		})
	}
}

// The acceptance criterion: a failure leaves the children in a known, reported
// place and never destroyed.
func TestRecloneReportsWhereTheChildrenAreOnFailure(t *testing.T) {
	system := withJailToReclone()
	system.cloneErr = errors.New("cannot clone 'tank/...@p5': out of space")

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err == nil {
		t.Fatal("RecloneRoot() returned no error")
	}
	if result.FailedAt != StepClone {
		t.Errorf("FailedAt = %q, want %q", result.FailedAt, StepClone)
	}

	// Both children and the root, each with where it actually is.
	want := []Parked{
		{Was: recloneRoot + "/data", Now: "tank/jails/retained/ottermq-data"},
		{Was: recloneRoot + "/logs", Now: "tank/jails/retained/ottermq-logs"},
		{Was: recloneRoot, Now: "tank/jails/retained/ottermq-root-base"},
	}
	if !reflect.DeepEqual(result.Parked, want) {
		t.Errorf("Parked =\n  %+v\nwant\n  %+v", result.Parked, want)
	}
	// And every name it reports is a dataset that really is there, which is
	// the difference between a recovery instruction and a guess.
	for _, parked := range want {
		found := false
		for _, dataset := range system.datasets {
			if dataset.Name == parked.Now {
				found = true
			}
		}
		if !found {
			t.Errorf("reported %s as parked at %s, which does not exist", parked.Was, parked.Now)
		}
	}
}

// A failure while putting the children back reports only the ones still out.
func TestRecloneReportsOnlyWhatIsStillOut(t *testing.T) {
	system := withJailToReclone()
	// Only the restore of the first child fails. Failing every mount would
	// break the retain long before the restore, which is what the first
	// version of this test actually measured.
	system.mountErr = errors.New("cannot mount: pool or dataset is busy")
	system.mountErrFor = map[string]bool{recloneRoot + "/data": true}

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err == nil {
		t.Fatal("RecloneRoot() returned no error")
	}
	if result.FailedAt != StepRestoreChildren {
		t.Fatalf("FailedAt = %q, want %q", result.FailedAt, StepRestoreChildren)
	}
	for _, parked := range result.Parked {
		if parked.Was == recloneRoot {
			t.Error("the root is reported as outstanding, but it was cloned back into place")
		}
	}
	if len(result.Parked) == 0 {
		t.Error("nothing reported as outstanding, but a restore failed")
	}
}

// A jail whose root was created directly rather than cloned has no origin to
// name the parked copy after.
func TestRecloneAJailThatWasNeverAClone(t *testing.T) {
	system := withJailToReclone()
	delete(system.origins, recloneRoot)

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err != nil {
		t.Fatalf("RecloneRoot() error = %v", err)
	}
	if result.From != "" {
		t.Errorf("From = %q, want empty for a root that was not a clone", result.From)
	}
	if result.OldRoot != "tank/jails/retained/ottermq-root" {
		t.Errorf("OldRoot = %q, want it parked without a snapshot suffix", result.OldRoot)
	}
}

// A jail with no child datasets is the simple case and must still work.
func TestRecloneAJailWithNoChildren(t *testing.T) {
	system := withJailToReclone()
	system.datasets = system.datasets[:3] // drop data and logs

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err != nil {
		t.Fatalf("RecloneRoot() error = %v", err)
	}
	if len(result.Children) != 0 || len(result.Parked) != 0 {
		t.Errorf("result = %+v, want no children and nothing outstanding", result)
	}
	if result.OldRoot == "" {
		t.Error("the old root was not parked")
	}
}

// Every command is audited, including the clone.
func TestRecloneIsAudited(t *testing.T) {
	logger := &fakeLogger{}
	if _, err := NewService(withJailToReclone(), logger, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate); err != nil {
		t.Fatalf("RecloneRoot() error = %v", err)
	}
	operations := map[string]int{}
	for _, entry := range logger.entries {
		operations[entry.Operation]++
	}
	if operations[OperationCloneDataset] != 1 {
		t.Errorf("clone-dataset appears %d times, want 1: %+v", operations[OperationCloneDataset], operations)
	}
	// Three renames out, three mount pairs, three renames back... the point is
	// that none of it is silent.
	if operations[OperationRenameDataset] < 5 {
		t.Errorf("only %d rename-dataset entries; every move must be audited: %+v",
			operations[OperationRenameDataset], operations)
	}
}
