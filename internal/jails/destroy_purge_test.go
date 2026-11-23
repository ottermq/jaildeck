package jails

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
)

const (
	rootSnap  = "tank/jails/containers/db1@auto_20261123_1000"
	childSnap = "tank/jails/containers/db1/data@auto_20261123_1000"
	handSnap  = "tank/jails/containers/db1@before-upgrade"
)

// Snapshots that all start with the prefix are destroyed, twice over: once
// before the stop and once just before the root (#280).
func TestDestroyPurgesTheSnapshotsItWasToldTo(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	var steps []string
	system.steps, destroyer.steps, remover.steps, autostart.steps = &steps, &steps, &steps, &steps
	destroyer.dependents.Snapshots = []string{rootSnap, childSnap}
	later := "tank/jails/containers/db1@auto_20261123_1015"
	destroyer.purges = [][]string{{rootSnap, childSnap}, {later}}

	result, err := destroyService(system, destroyer, remover, autostart, logger).
		DestroyPurging(context.Background(), "db1", "db1", "auto_")
	if err != nil || result.Blocked != nil || !result.Destroyed {
		t.Fatalf("result %+v, err %v", result, err)
	}
	if want := []string{"purge", "stop", "purge", "autostart", "remove-config", "destroy-root"}; !slices.Equal(steps, want) {
		t.Errorf("steps = %v, want %v", steps, want)
	}
	if want := []string{rootSnap, childSnap, later}; !slices.Equal(result.Purged, want) {
		t.Errorf("Purged = %v, want %v", result.Purged, want)
	}
	if !slices.Equal(destroyer.purgedWith, []string{"auto_", "auto_"}) {
		t.Errorf("purged with %v", destroyer.purgedWith)
	}
}

// Anything but the prefix's snapshots still refuses, and nothing runs.
func TestAPurgeOnlyHappensWhenNothingElseIsInTheWay(t *testing.T) {
	for what, arrange := range map[string]func(*RootDependents){
		"a snapshot without the prefix": func(d *RootDependents) { d.Snapshots = append(d.Snapshots, handSnap) },
		"a clone":                       func(d *RootDependents) { d.Clones = []string{"tank/jails/containers/db2"} },
		"a hold":                        func(d *RootDependents) { d.Holds = []string{rootSnap + " (keep)"} },
	} {
		system, destroyer, remover, autostart, logger := destroyable()
		destroyer.dependents.Snapshots = []string{rootSnap, childSnap}
		arrange(&destroyer.dependents)

		result, err := destroyService(system, destroyer, remover, autostart, logger).
			DestroyPurging(context.Background(), "db1", "db1", "auto_")
		if err != nil || result.Blocked == nil {
			t.Errorf("%s: result %+v, err %v; want blocked", what, result, err)
			continue
		}
		if len(destroyer.purgedWith)+len(system.stopped)+len(destroyer.destroyed) != 0 {
			t.Errorf("%s: purged %v stopped %v destroyed %v", what, destroyer.purgedWith, system.stopped, destroyer.destroyed)
		}
		if !strings.Contains(result.Blocked.Reason, "purge_snapshots=auto_ would take 2 snapshot(s)") {
			t.Errorf("%s: reason %q", what, result.Blocked.Reason)
		}
		if slices.Contains(result.Blocked.Snapshots, rootSnap) {
			t.Errorf("%s: lists %s, which the purge would take, as in the way", what, rootSnap)
		}
	}
}

// Without the option a destroy never purges, whatever the snapshots are called.
func TestDestroyWithoutAPrefixPurgesNothing(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	destroyer.dependents.Snapshots = []string{rootSnap}
	result, _ := destroyService(system, destroyer, remover, autostart, logger).
		Destroy(context.Background(), "db1", "db1")
	if result.Blocked == nil || len(destroyer.purgedWith) != 0 {
		t.Errorf("result %+v, purged with %v", result, destroyer.purgedWith)
	}
}

// A first purge that fails leaves the jail running and configured, and says
// what it did destroy.
func TestAFailedPurgeLeavesTheJailAsItWas(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	destroyer.dependents.Snapshots = []string{rootSnap, childSnap}
	destroyer.purges = [][]string{{rootSnap}}
	destroyer.purgeErr, destroyer.purgeErrAt = errors.New("dataset is busy"), 1

	result, err := destroyService(system, destroyer, remover, autostart, logger).
		DestroyPurging(context.Background(), "db1", "db1", "auto_")
	if err == nil || result.FailedAt != StepPurge {
		t.Fatalf("result %+v, err %v", result, err)
	}
	if !slices.Equal(result.Purged, []string{rootSnap}) {
		t.Errorf("Purged = %v", result.Purged)
	}
	if result.Stopped || result.Unlisted || len(remover.removed) != 0 || len(destroyer.destroyed) != 0 {
		t.Errorf("went on after the purge failed: %+v", result)
	}
}

// The purge after the stop still runs when nothing matched at the check --
// the schedule may have taken one while the jail stopped -- and a failure
// there leaves the jail configured, so the same call can be repeated.
func TestAPurgeAfterTheStopThatFailsLeavesTheJailConfigured(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	destroyer.purgeErr, destroyer.purgeErrAt = errors.New("dataset is busy"), 1

	result, err := destroyService(system, destroyer, remover, autostart, logger).
		DestroyPurging(context.Background(), "db1", "db1", "auto_")
	if err == nil || result.FailedAt != StepPurge || result.Destroyed || len(destroyer.destroyed) != 0 {
		t.Fatalf("result %+v, err %v", result, err)
	}
	if !result.Stopped || result.Unlisted || result.Config != "" || len(remover.removed) != 0 || result.Root == "" {
		t.Errorf("result %+v: want stopped, still configured, root named", result)
	}
}

func TestAPurgePrefixMustBeASnapshotName(t *testing.T) {
	for _, prefix := range []string{"a b", "x@y", "x/y", "*"} {
		system, destroyer, remover, autostart, logger := destroyable()
		_, err := destroyService(system, destroyer, remover, autostart, logger).
			DestroyPurging(context.Background(), "db1", "db1", prefix)
		if appCode(t, err) != http.StatusBadRequest || len(destroyer.checked) != 0 {
			t.Errorf("%q: %v", prefix, err)
		}
	}
}
