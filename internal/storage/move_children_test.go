package storage

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

// A dataset with a child and a grandchild of its own, all mounted.
func withNestedData() *stubSystem {
	system := withRetainable()
	system.datasets = append(system.datasets,
		Dataset{Name: ottermqData + "/wal", Mountpoint: "/usr/local/jails/containers/ottermq/data/wal"},
		Dataset{Name: ottermqData + "/wal/archive", Mountpoint: "/usr/local/jails/containers/ottermq/data/wal/archive"})
	return system
}

// zfs unmount takes the datasets beneath down too, and zfs mount brings back
// only the one it is given: each is mounted again, parents first (#294).
func TestAMoveRemountsWhatWasMountedBeneath(t *testing.T) {
	system := withNestedData()
	logger := &fakeLogger{}

	moved, err := NewService(system, logger, "tank").RetainDataset(context.Background(), ottermqData, "kept")
	if err != nil {
		t.Fatalf("RetainDataset: %v", err)
	}
	to := "tank/jails/retained/kept"
	want := []string{
		"zfs create tank/jails/retained",
		"zfs unmount " + ottermqData,
		"zfs rename -u " + ottermqData + " " + to,
		"zfs mount " + to,
		"zfs mount " + to + "/wal",
		"zfs mount " + to + "/wal/archive",
	}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran\n  %q\nwant\n  %q", system.ran, want)
	}
	if want := []string{to + "/wal", to + "/wal/archive"}; !slices.Equal(moved.RemountedChildren, want) {
		t.Errorf("RemountedChildren = %v, want %v", moved.RemountedChildren, want)
	}
	if len(logger.entries) != len(want) {
		t.Errorf("%d audit entries, want one per command", len(logger.entries))
	}
}

// One that was not mounted before the move is not mounted after it.
func TestAMoveLeavesAnUnmountedChildUnmounted(t *testing.T) {
	system := withNestedData()
	system.notMounted[ottermqData+"/wal/archive"] = true

	moved, err := NewService(system, &fakeLogger{}, "tank").RetainDataset(context.Background(), ottermqData, "kept")
	if err != nil {
		t.Fatalf("RetainDataset: %v", err)
	}
	if slices.Contains(system.ran, "zfs mount tank/jails/retained/kept/wal/archive") {
		t.Errorf("mounted a child that was not mounted: %q", system.ran)
	}
	if !slices.Equal(moved.RemountedChildren, []string{"tank/jails/retained/kept/wal"}) {
		t.Errorf("RemountedChildren = %v", moved.RemountedChildren)
	}
}

// A child whose mountpoint is set on itself still names the place it left:
// it, and what is beneath it, are left unmounted and said to be.
func TestAMoveDoesNotMountAChildBackWhereItLeft(t *testing.T) {
	system := withNestedData()
	system.properties = append(system.properties, Property{
		Dataset: ottermqData + "/wal", Name: "mountpoint", Value: "/usr/local/jails/containers/ottermq/var/wal"})

	moved, err := NewService(system, &fakeLogger{}, "tank").RetainDataset(context.Background(), ottermqData, "kept")
	if err != nil {
		t.Fatalf("RetainDataset: %v", err)
	}
	to := "tank/jails/retained/kept"
	if want := []string{to + "/wal", to + "/wal/archive"}; !slices.Equal(moved.LeftUnmounted, want) {
		t.Errorf("LeftUnmounted = %v, want %v", moved.LeftUnmounted, want)
	}
	if len(moved.RemountedChildren) != 0 || slices.Contains(system.ran, "zfs mount "+to+"/wal") {
		t.Errorf("remounted %v, ran %q", moved.RemountedChildren, system.ran)
	}
}

// A child's mount that fails is the error, with the move reported.
func TestAFailedChildMountIsReported(t *testing.T) {
	system := withNestedData()
	to := "tank/jails/retained/kept"
	system.mountErr = errors.New("cannot mount: directory is not empty")
	system.mountErrFor = map[string]bool{to + "/wal/archive": true}
	system.mountOnly = true

	moved, err := NewService(system, &fakeLogger{}, "tank").RetainDataset(context.Background(), ottermqData, "kept")
	if err == nil {
		t.Fatal("want the mount's failure")
	}
	if moved.To != to || !slices.Equal(moved.RemountedChildren, []string{to + "/wal"}) {
		t.Errorf("moved = %+v", moved)
	}
}

// A failed rename mounts the dataset and what was beneath it back where they
// were -- a child with its own mountpoint too, since there it is right.
func TestAFailedRenameRemountsTheChildrenWhereTheyWere(t *testing.T) {
	system := withNestedData()
	system.properties = append(system.properties, Property{
		Dataset: ottermqData + "/wal", Name: "mountpoint", Value: "/usr/local/jails/containers/ottermq/var/wal"})
	system.datasets = append(system.datasets, Dataset{Name: "tank/jails/retained", Mountpoint: "/usr/local/jails/retained"})
	system.writeErr = errors.New("cannot rename: permission denied")

	if _, err := NewService(system, &fakeLogger{}, "tank").RetainDataset(context.Background(), ottermqData, "kept"); err == nil {
		t.Fatal("want the rename's failure")
	}
	for _, name := range []string{ottermqData, ottermqData + "/wal", ottermqData + "/wal/archive"} {
		if system.notMounted[name] {
			t.Errorf("%s left unmounted after the rename failed", name)
		}
	}
}

// A mountpoint change on the moved dataset mounts what inherits it (#121);
// one beneath that was not mounted before is unmounted again.
func TestAMountpointChangeDoesNotLeaveAnUnmountedChildMounted(t *testing.T) {
	system := withNestedData()
	system.properties = append(system.properties, Property{
		Dataset: ottermqData, Name: "mountpoint", Value: "/usr/local/jails/containers/ottermq/var/db"})
	system.notMounted[ottermqData+"/wal"] = true
	system.notMounted[ottermqData+"/wal/archive"] = true

	moved, err := NewService(system, &fakeLogger{}, "tank").RetainDataset(context.Background(), ottermqData, "kept")
	if err != nil {
		t.Fatalf("RetainDataset: %v", err)
	}
	for _, name := range []string{"tank/jails/retained/kept/wal", "tank/jails/retained/kept/wal/archive"} {
		if !system.notMounted[name] {
			t.Errorf("%s is mounted after the move; it was not before", name)
		}
	}
	if len(moved.RemountedChildren)+len(moved.LeftUnmounted) != 0 {
		t.Errorf("moved = %+v", moved)
	}
}

// An unmounted dataset with its own mountpoint still makes the mounted ones
// beneath it, which inherit that path, stale.
func TestAnUnmountedAncestorWithItsOwnMountpointMakesItsChildrenStale(t *testing.T) {
	system := withNestedData()
	system.properties = append(system.properties, Property{
		Dataset: ottermqData + "/wal", Name: "mountpoint", Value: "/usr/local/jails/containers/ottermq/var/wal"})
	system.notMounted[ottermqData+"/wal"] = true

	moved, err := NewService(system, &fakeLogger{}, "tank").RetainDataset(context.Background(), ottermqData, "kept")
	if err != nil {
		t.Fatalf("RetainDataset: %v", err)
	}
	if !slices.Equal(moved.LeftUnmounted, []string{"tank/jails/retained/kept/wal/archive"}) {
		t.Errorf("LeftUnmounted = %v", moved.LeftUnmounted)
	}
	if slices.Contains(system.ran, "zfs mount tank/jails/retained/kept/wal/archive") {
		t.Errorf("mounted archive back under the jail it left: %q", system.ran)
	}
}

// Restored under a jail, a child's own mountpoint inside that jail is right.
func TestARestoreMountsAChildWhoseMountpointIsInTheJail(t *testing.T) {
	system := withNestedData()
	system.properties = append(system.properties, Property{
		Dataset: ottermqData + "/wal", Name: "mountpoint", Value: "/usr/local/jails/containers/ottermq/var/wal"})
	service := NewService(system, &fakeLogger{}, "tank")
	if _, err := service.RetainDataset(context.Background(), ottermqData, "kept"); err != nil {
		t.Fatal(err)
	}
	// Mounted as it stands in retained/, as an operator who set it would.
	delete(system.notMounted, "tank/jails/retained/kept/wal")
	delete(system.notMounted, "tank/jails/retained/kept/wal/archive")

	moved, err := service.RestoreDataset(context.Background(), "tank", "kept", ottermqData)
	if err != nil {
		t.Fatalf("RestoreDataset: %v", err)
	}
	if len(moved.LeftUnmounted) != 0 || !slices.Contains(moved.RemountedChildren, ottermqData+"/wal") {
		t.Errorf("moved = %+v", moved)
	}
}

// A mount state that cannot be read is the error, not a guess.
func TestAMountStateThatCannotBeReadIsTheError(t *testing.T) {
	system := withNestedData()
	service := NewService(system, &fakeLogger{}, "tank")
	system.mountedErrAfter = 1 + 2 // the dataset itself, then wal and archive before the move
	system.mountedErr = errors.New("zfs get mounted: timed out")

	moved, err := service.RetainDataset(context.Background(), ottermqData, "kept")
	if err == nil || moved.To == "" {
		t.Fatalf("moved %+v, err %v; want the read's failure after the move", moved, err)
	}
}

// A re-clone puts a child back where it was, so a dataset beneath it whose
// own mountpoint names the jail -- left unmounted while the child sat in
// retained/ -- is mounted again, and nothing is reported left (#294).
func TestARecloneRemountsWhatTheRetainLeft(t *testing.T) {
	system := withJailToReclone()
	// The containers parent, so the new root's clone inherits the jail's
	// path the way zfs gives it one.
	system.datasets = append(system.datasets,
		Dataset{Name: "tank/jails/containers", Mountpoint: "/usr/local/jails/containers"},
		Dataset{Name: recloneRoot + "/data/wal", Mountpoint: "/usr/local/jails/containers/ottermq/var/wal"})
	system.properties = append(system.properties, Property{
		Dataset: recloneRoot + "/data/wal", Name: "mountpoint", Value: "/usr/local/jails/containers/ottermq/var/wal"})

	result, err := NewService(system, &fakeLogger{}, "tank").RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err != nil {
		t.Fatalf("RecloneRoot: %v (%+v)", err, result)
	}
	if len(result.LeftUnmounted) != 0 {
		t.Errorf("LeftUnmounted = %v", result.LeftUnmounted)
	}
	if system.notMounted[recloneRoot+"/data/wal"] {
		t.Errorf("wal is unmounted after the re-clone; ran %q", system.ran)
	}
}
