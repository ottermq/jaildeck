package storage

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// A jail with a schedule's snapshots on its root and its child, and one taken
// by hand.
func withScheduledSnapshots() *stubSystem {
	system := withAJail()
	system.datasets = append(system.datasets, Dataset{Name: jailRoot + "/data", Mountpoint: jailPath + "/data"})
	system.snapshots = append(system.snapshots,
		Snapshot{Name: jailRoot + "@auto_1", Dataset: jailRoot, Short: "auto_1", CreateTXG: 20},
		Snapshot{Name: jailRoot + "/data@auto_1", Dataset: jailRoot + "/data", Short: "auto_1", CreateTXG: 20},
		Snapshot{Name: jailRoot + "@by-hand", Dataset: jailRoot, Short: "by-hand", CreateTXG: 21},
	)
	return system
}

// Each matching snapshot goes in its own destroy, never -r, and is audited
// (#280).
func TestPurgeSnapshotsTakesThePrefix(t *testing.T) {
	system := withScheduledSnapshots()
	system.snapshots = slices.DeleteFunc(system.snapshots, func(s Snapshot) bool { return s.Short == "by-hand" })
	logger := &fakeLogger{}

	purged, err := jailSourceFor(system, logger).PurgeSnapshots(context.Background(), jailPath, "auto_")
	if err != nil {
		t.Fatalf("PurgeSnapshots: %v", err)
	}
	if want := []string{jailRoot + "@auto_1", jailRoot + "/data@auto_1"}; !slices.Equal(purged, want) {
		t.Errorf("purged %v, want %v", purged, want)
	}
	if want := []string{"zfs destroy " + jailRoot + "@auto_1", "zfs destroy " + jailRoot + "/data@auto_1"}; !slices.Equal(system.ran, want) {
		t.Errorf("ran %q, want %q", system.ran, want)
	}
	if len(logger.entries) != 2 {
		t.Errorf("%d audit entries, want one per snapshot", len(logger.entries))
	}
	left, _ := NewService(system, &fakeLogger{}, "tank").SnapshotsOf(context.Background(), jailRoot)
	if len(left) != 0 {
		t.Errorf("left %+v", left)
	}
}

// A snapshot without the prefix in the fresh listing -- taken since the
// caller checked -- stops the purge before it destroys anything.
func TestPurgeSnapshotsRefusesWhenSomethingElseAppeared(t *testing.T) {
	system := withScheduledSnapshots()
	purged, err := jailSourceFor(system, &fakeLogger{}).PurgeSnapshots(context.Background(), jailPath, "auto_")
	if appErrCode(t, err) != 409 || len(purged) != 0 || len(system.ran) != 0 {
		t.Errorf("purged %v, ran %q, err %v", purged, system.ran, err)
	}
}

// A destroy that fails part way stops there, and what went before it is
// returned.
func TestPurgeSnapshotsReturnsWhatWentBeforeAFailure(t *testing.T) {
	system := withScheduledSnapshots()
	system.snapshots = slices.DeleteFunc(system.snapshots, func(s Snapshot) bool { return s.Short == "by-hand" })
	system.writeErrOn = map[string]bool{jailRoot + "/data@auto_1": true}
	purged, err := jailSourceFor(system, &fakeLogger{}).PurgeSnapshots(context.Background(), jailPath, "auto_")
	if err == nil || !slices.Equal(purged, []string{jailRoot + "@auto_1"}) {
		t.Errorf("purged %v, err %v", purged, err)
	}
}

// A destroy that fails stops there, and what went before it is returned.
func TestPurgeSnapshotsStopsAtAFailure(t *testing.T) {
	system := withScheduledSnapshots()
	system.snapshots = slices.DeleteFunc(system.snapshots, func(s Snapshot) bool { return s.Short == "by-hand" })
	system.writeErr = errors.New("cannot destroy snapshot: dataset is busy")

	purged, err := jailSourceFor(system, &fakeLogger{}).PurgeSnapshots(context.Background(), jailPath, "auto_")
	if err == nil || len(purged) != 0 || len(system.ran) != 1 {
		t.Errorf("purged %v, ran %q, err %v", purged, system.ran, err)
	}
}

func TestPurgeSnapshotsWithNoPrefixDoesNothing(t *testing.T) {
	system := withScheduledSnapshots()
	purged, err := jailSourceFor(system, &fakeLogger{}).PurgeSnapshots(context.Background(), jailPath, "")
	if err != nil || len(purged) != 0 || len(system.ran) != 0 {
		t.Errorf("purged %v, ran %q, err %v", purged, system.ran, err)
	}
}
