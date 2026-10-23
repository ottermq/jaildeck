package storage

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// withSnapshots is a jail dataset with a child, and three snapshots on the
// jail's own dataset in a known transaction-group order.
func withSnapshots() *stubSystem {
	system := &stubSystem{
		pools: []string{"tank"},
		datasets: []Dataset{
			{Name: "tank/jails/containers/ottermq", Mountpoint: "/usr/local/jails/containers/ottermq"},
			{Name: "tank/jails/containers/ottermq/data", Mountpoint: "/usr/local/jails/containers/ottermq/data"},
		},
		clones: map[string][]string{},
		// Deliberately out of transaction-group order in the slice, so a test
		// that happens to work on insertion order fails.
		snapshots: []Snapshot{
			{Name: "tank/jails/containers/ottermq@three", Dataset: "tank/jails/containers/ottermq", Short: "three", CreateTXG: 30},
			{Name: "tank/jails/containers/ottermq@one", Dataset: "tank/jails/containers/ottermq", Short: "one", CreateTXG: 10},
			{Name: "tank/jails/containers/ottermq@two", Dataset: "tank/jails/containers/ottermq", Short: "two", CreateTXG: 20},
			{Name: "tank/jails/containers/ottermq/data@one", Dataset: "tank/jails/containers/ottermq/data", Short: "one", CreateTXG: 11},
		},
		nextTXG: 100,
	}
	return system
}

const jailDataset = "tank/jails/containers/ottermq"

func TestCreateSnapshotRecursively(t *testing.T) {
	system := withSnapshots()
	logger := &fakeLogger{}

	result, err := NewService(system, logger, "tank").
		CreateSnapshot(context.Background(), jailDataset, "before-upgrade", true)
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	// -r is what makes the jail and its data dataset one point in time, which
	// is the whole reason the jail route is recursive (JD-007).
	want := []string{"zfs snapshot -r " + jailDataset + "@before-upgrade"}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran %q, want %q", system.ran, want)
	}
	wantCreated := []string{jailDataset + "@before-upgrade", jailDataset + "/data@before-upgrade"}
	if !reflect.DeepEqual(result.Created, wantCreated) {
		t.Errorf("Created = %q, want %q", result.Created, wantCreated)
	}
	if len(logger.entries) != 1 || logger.entries[0].Operation != OperationCreateSnapshot {
		t.Errorf("audit = %+v, want one create-snapshot", logger.entries)
	}
}

// The retry after a timeout: it must not fail, and it must say nothing was made.
func TestCreateSnapshotIsIdempotent(t *testing.T) {
	system := withSnapshots()
	result, err := NewService(system, &fakeLogger{}, "tank").
		CreateSnapshot(context.Background(), jailDataset, "one", true)
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	if !result.Existed || len(result.Created) != 0 {
		t.Errorf("result = %+v, want existed with nothing created", result)
	}
	if len(system.ran) != 0 {
		t.Errorf("ran %q for a snapshot that was already there", system.ran)
	}
}

// The acceptance criterion: a rollback that would destroy newer snapshots
// refuses, and names them, until told explicitly.
func TestRollbackRefusesToDestroyNewerSnapshots(t *testing.T) {
	system := withSnapshots()
	service := NewService(system, &fakeLogger{}, "tank")
	full := jailDataset + "@one"

	result, err := service.Rollback(context.Background(), jailDataset, "one", full, false)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if result.Blocked == nil {
		t.Fatalf("result = %+v, want it blocked", result)
	}
	want := []string{jailDataset + "@two", jailDataset + "@three"}
	if !reflect.DeepEqual(result.Blocked.Newer, want) {
		t.Errorf("Newer = %q, want %q", result.Blocked.Newer, want)
	}
	// The child's snapshot is newer than @one by transaction group, and is not
	// in the list: zfs rollback never touches another dataset.
	for _, name := range result.Blocked.Newer {
		if name == jailDataset+"/data@one" {
			t.Error("a child's snapshot was listed as something rollback would destroy")
		}
	}
	if len(system.ran) != 0 {
		t.Errorf("ran %q while refusing, want nothing", system.ran)
	}

	// Told explicitly, it goes ahead and reports exactly what it took.
	result, err = service.Rollback(context.Background(), jailDataset, "one", full, true)
	if err != nil {
		t.Fatalf("Rollback(destroyNewer) error = %v", err)
	}
	if result.Blocked != nil {
		t.Fatalf("still blocked: %+v", result.Blocked)
	}
	if !reflect.DeepEqual(result.Destroyed, want) {
		t.Errorf("Destroyed = %q, want %q", result.Destroyed, want)
	}
	if len(system.ran) != 1 || system.ran[0] != "zfs rollback -r "+full {
		t.Errorf("ran %q, want one rollback -r", system.ran)
	}
}

// Rolling back to the newest snapshot destroys nothing, so it needs no
// permission to destroy anything.
func TestRollbackToTheNewestNeedsNoPermission(t *testing.T) {
	system := withSnapshots()
	full := jailDataset + "@three"
	result, err := NewService(system, &fakeLogger{}, "tank").
		Rollback(context.Background(), jailDataset, "three", full, false)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if result.Blocked != nil || len(result.Destroyed) != 0 {
		t.Errorf("result = %+v, want it to go ahead destroying nothing", result)
	}
	if len(system.ran) != 1 || system.ran[0] != "zfs rollback "+full {
		t.Errorf("ran %q, want a rollback with no -r", system.ran)
	}
}

// Ordering is by transaction group, not by timestamp. Two snapshots taken in
// the same second share a creation time, and that is exactly when getting this
// wrong destroys something it should not have.
func TestRollbackOrdersByTransactionGroupNotTime(t *testing.T) {
	system := withSnapshots()
	same := time.Unix(1792700000, 0)
	for i := range system.snapshots {
		system.snapshots[i].Created = same
	}
	result, err := NewService(system, &fakeLogger{}, "tank").
		Rollback(context.Background(), jailDataset, "one", jailDataset+"@one", false)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if result.Blocked == nil || len(result.Blocked.Newer) != 2 {
		t.Errorf("result = %+v, want two newer snapshots found despite identical timestamps", result)
	}
}

func TestDestroySnapshotRefusesCloneAndHold(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*stubSystem)
		wantIn  string
	}{
		{
			name:    "a dependent clone",
			arrange: func(s *stubSystem) { s.clones[jailDataset+"@one"] = []string{"tank/jails/containers/copy"} },
			wantIn:  "tank/jails/containers/copy",
		},
		{
			name:    "a hold",
			arrange: func(s *stubSystem) { s.holds = []Hold{{Snapshot: jailDataset + "@one", Tag: "keep-for-audit"}} },
			wantIn:  "keep-for-audit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withSnapshots()
			tt.arrange(system)

			result, err := NewService(system, &fakeLogger{}, "tank").
				DestroySnapshot(context.Background(), jailDataset, "one", jailDataset+"@one", false)
			if err != nil {
				t.Fatalf("DestroySnapshot() error = %v", err)
			}
			if result.Blocked == nil {
				t.Fatalf("result = %+v, want it blocked", result)
			}
			named := append(append([]string{}, result.Blocked.Clones...), result.Blocked.Holds...)
			found := false
			for _, name := range named {
				if strings.Contains(name, tt.wantIn) {
					found = true
				}
			}
			if !found {
				t.Errorf("blocked = %+v, want it to name %q", result.Blocked, tt.wantIn)
			}
			// Checked before anything destructive ran, not reported after it
			// failed.
			if len(system.ran) != 0 {
				t.Errorf("ran %q while refusing, want nothing", system.ran)
			}
		})
	}
}

// A clone on a snapshot the rollback would destroy stops it too: getting past
// that needs zfs destroy -R, which takes the clone with it.
func TestRollbackRefusesWhenANewerSnapshotHasAClone(t *testing.T) {
	system := withSnapshots()
	system.clones[jailDataset+"@two"] = []string{"tank/jails/containers/copy"}

	result, err := NewService(system, &fakeLogger{}, "tank").
		Rollback(context.Background(), jailDataset, "one", jailDataset+"@one", true)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if result.Blocked == nil || len(result.Blocked.Clones) == 0 {
		t.Errorf("result = %+v, want it blocked by the clone", result)
	}
	if len(system.ran) != 0 {
		t.Errorf("ran %q, want nothing", system.ran)
	}
}

func TestConfirmIsRequiredAndExact(t *testing.T) {
	full := jailDataset + "@one"
	tests := []struct {
		name    string
		confirm string
	}{
		{name: "absent", confirm: ""},
		{name: "just the short name", confirm: "one"},
		{name: "another jail's snapshot of the same name", confirm: "tank/jails/containers/postgres@one"},
		{name: "near miss", confirm: full + " "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withSnapshots()
			service := NewService(system, &fakeLogger{}, "tank")

			_, destroyErr := service.DestroySnapshot(context.Background(), jailDataset, "one", tt.confirm, false)
			_, rollbackErr := service.Rollback(context.Background(), jailDataset, "one", tt.confirm, true)
			for _, err := range []error{destroyErr, rollbackErr} {
				if err == nil {
					t.Fatalf("confirm=%q was accepted", tt.confirm)
				}
				if code := appErrCode(t, err); code != http.StatusBadRequest {
					t.Errorf("error code = %d, want 400", code)
				}
			}
			if len(system.ran) != 0 {
				t.Errorf("ran %q without a valid confirmation", system.ran)
			}
		})
	}
}

func TestSnapshotNotFound(t *testing.T) {
	system := withSnapshots()
	service := NewService(system, &fakeLogger{}, "tank")
	full := jailDataset + "@nope"

	if _, err := service.DestroySnapshot(context.Background(), jailDataset, "nope", full, false); err == nil {
		t.Error("DestroySnapshot() of an absent snapshot returned no error")
	} else if code := appErrCode(t, err); code != http.StatusNotFound {
		t.Errorf("destroy error code = %d, want 404", code)
	}
	if _, err := service.Rollback(context.Background(), jailDataset, "nope", full, false); err == nil {
		t.Error("Rollback() to an absent snapshot returned no error")
	} else if code := appErrCode(t, err); code != http.StatusNotFound {
		t.Errorf("rollback error code = %d, want 404", code)
	}
}
