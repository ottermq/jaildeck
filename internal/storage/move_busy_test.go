package storage

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

var errBusy = errors.New("cannot unmount: pool or dataset is busy")

func hasDataset(system *stubSystem, name string) bool {
	return slices.ContainsFunc(system.datasets, func(d Dataset) bool { return d.Name == name })
}

// A dataset still in use is refused before the rename, so it is still under
// its own name, mounted where it was (#281).
func TestMoveRefusesABusyDatasetBeforeRenamingIt(t *testing.T) {
	system := withRetainable()
	system.mountErr = errBusy

	if _, err := NewService(system, &fakeLogger{}, "tank").
		RetainDataset(context.Background(), ottermqData, "ottermq-data"); err == nil {
		t.Fatal("RetainDataset() of a busy dataset returned no error")
	}
	for _, command := range system.ran {
		if strings.HasPrefix(command, "zfs rename") {
			t.Errorf("ran %q after the unmount was refused", command)
		}
	}
	if !hasDataset(system, ottermqData) {
		t.Errorf("%s is gone after a refused move", ottermqData)
	}
	if system.notMounted[ottermqData] {
		t.Errorf("%s was left unmounted", ottermqData)
	}
}

// A rename that fails after the unmount mounts the dataset back where it was.
func TestMoveMountsBackWhenTheRenameFails(t *testing.T) {
	system := withRetainable()
	system.datasets = append(system.datasets,
		Dataset{Name: "tank/jails/retained", Mountpoint: "/usr/local/jails/retained"},
		Dataset{Name: "tank/jails/retained/ottermq-data", Mountpoint: "/usr/local/jails/retained/ottermq-data"})
	from := "tank/jails/retained/ottermq-data"

	// No jail named nowhere: the rename has no parent to go under.
	if _, err := NewService(system, &fakeLogger{}, "tank").
		RestoreDataset(context.Background(), "tank", "ottermq-data", "tank/jails/containers/nowhere/data"); err == nil {
		t.Fatal("RestoreDataset() into a missing jail returned no error")
	}
	want := []string{"zfs unmount " + from, "zfs rename -u " + from + " tank/jails/containers/nowhere/data", "zfs mount " + from}
	if !slices.Equal(system.ran, want) {
		t.Errorf("ran\n  %q\nwant\n  %q", system.ran, want)
	}
	if system.notMounted[from] {
		t.Errorf("%s was left unmounted", from)
	}
}

// A root still in use stays the jail's root: the re-clone stops at park-root
// with the root untouched, and reports only the children as parked.
func TestRecloneLeavesABusyRootInPlace(t *testing.T) {
	system := withJailToReclone()
	system.mountErr = errBusy
	system.mountErrFor = map[string]bool{recloneRoot: true}

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err == nil {
		t.Fatal("RecloneRoot() with a busy root returned no error")
	}
	if result.FailedAt != StepParkRoot {
		t.Fatalf("FailedAt = %q, want %q", result.FailedAt, StepParkRoot)
	}
	if !hasDataset(system, recloneRoot) {
		t.Errorf("%s was moved although it was busy", recloneRoot)
	}
	for _, parked := range result.Parked {
		if parked.Was == recloneRoot {
			t.Errorf("the root is reported parked at %s, but it never moved", parked.Now)
		}
	}
	if len(result.Parked) != 2 {
		t.Errorf("Parked = %v, want both children", result.Parked)
	}
}

// A root that failed after its rename has moved, and parked says where.
func TestRecloneReportsARootThatMovedBeforeItsMoveFailed(t *testing.T) {
	system := withJailToReclone()
	parkedAt := "tank/jails/retained/" + recloneRootName(recloneRoot, oldTemplate)
	system.mountErr = errors.New("cannot mount: out of luck")
	system.mountErrFor = map[string]bool{parkedAt: true}
	system.mountOnly = true

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err == nil {
		t.Fatal("RecloneRoot() returned no error")
	}
	if result.FailedAt != StepParkRoot {
		t.Fatalf("FailedAt = %q, want %q", result.FailedAt, StepParkRoot)
	}
	if !slices.Contains(result.Parked, Parked{Was: recloneRoot, Now: parkedAt}) {
		t.Errorf("Parked = %v, want the root at %s", result.Parked, parkedAt)
	}
}

// A child whose move failed after its rename is reported where it went.
func TestRecloneReportsAChildThatMovedBeforeItsMoveFailed(t *testing.T) {
	system := withJailToReclone()
	parkedAt := "tank/jails/retained/" + recloneRetainName(recloneRoot, recloneRoot+"/data")
	system.mountErr = errors.New("cannot mount: out of luck")
	system.mountErrFor = map[string]bool{parkedAt: true}
	system.mountOnly = true

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err == nil {
		t.Fatal("RecloneRoot() returned no error")
	}
	if result.FailedAt != StepRetainChildren {
		t.Fatalf("FailedAt = %q, want %q", result.FailedAt, StepRetainChildren)
	}
	if !slices.Contains(result.Parked, Parked{Was: recloneRoot + "/data", Now: parkedAt}) {
		t.Errorf("Parked = %v, want data at %s", result.Parked, parkedAt)
	}
}

// A child renamed back under the jail whose mount then failed is not
// reported as still in retained/.
func TestRecloneDoesNotReportARestoredChildAsParked(t *testing.T) {
	system := withJailToReclone()
	system.mountErr = errors.New("cannot mount: out of luck")
	system.mountErrFor = map[string]bool{recloneRoot + "/data": true}
	system.mountOnly = true

	result, _ := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if result.FailedAt != StepRestoreChildren {
		t.Fatalf("FailedAt = %q, want %q", result.FailedAt, StepRestoreChildren)
	}
	for _, parked := range result.Parked {
		if parked.Was == recloneRoot+"/data" {
			t.Errorf("data reported parked at %s, but it is back under the jail", parked.Now)
		}
	}
	if !hasDataset(system, recloneRoot+"/data") {
		t.Fatal("data is not back under the jail")
	}
}

// Both failures reach the client when the rename and the mount back fail.
func TestMoveReportsTheRenameAndTheFailedMountBack(t *testing.T) {
	system := withRetainable()
	system.datasets = append(system.datasets,
		Dataset{Name: "tank/jails/retained", Mountpoint: "/usr/local/jails/retained"},
		Dataset{Name: "tank/jails/retained/ottermq-data", Mountpoint: "/usr/local/jails/retained/ottermq-data"})
	system.mountErr = errors.New("cannot mount: out of luck")
	system.mountOnly = true

	_, err := NewService(system, &fakeLogger{}, "tank").
		RestoreDataset(context.Background(), "tank", "ottermq-data", "tank/jails/containers/nowhere/data")
	if err == nil || !strings.Contains(err.Error(), "parent does not exist") ||
		!strings.Contains(err.Error(), "mounting it back") {
		t.Errorf("error = %v, want the rename's and the mount's", err)
	}
}
