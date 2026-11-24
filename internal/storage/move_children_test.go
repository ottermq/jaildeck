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

// A failed rename mounts the dataset and what was beneath it back where they were.
func TestAFailedRenameRemountsTheChildrenWhereTheyWere(t *testing.T) {
	system := withNestedData()
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
