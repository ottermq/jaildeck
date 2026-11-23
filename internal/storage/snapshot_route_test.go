package storage

import (
	"context"
	"reflect"
	"testing"
)

const jailMountpoint = "/usr/local/jails/containers/ottermq"

func withChildSnapshot() *stubSystem {
	system := withConfiguredJail()
	system.datasets = append(system.datasets,
		Dataset{Name: configuredJail + "/etc", Mountpoint: jailMountpoint + "/etc"})
	system.snapshots = append(system.snapshots,
		Snapshot{Name: configuredJail + "@auto_1", Dataset: configuredJail, Short: "auto_1", CreateTXG: 50},
		Snapshot{Name: configuredJail + "/etc@auto_2", Dataset: configuredJail + "/etc", Short: "auto_2", CreateTXG: 51})
	return system
}

// The jail's snapshot route takes a child's snapshot too, named in full by
// confirm, and destroys it alone (#280).
func TestTheRootRouteDestroysAChildsSnapshotNamedInFull(t *testing.T) {
	system := withChildSnapshot()
	result, err := NewJailSource(NewService(system, &fakeLogger{}, "tank")).
		DestroySnapshot(context.Background(), jailMountpoint, "", "auto_2", configuredJail+"/etc@auto_2", true)
	if err != nil {
		t.Fatalf("DestroySnapshot() error = %v", err)
	}
	if len(result.Destroyed) == 0 {
		t.Errorf("result = %+v, want it destroyed", result)
	}
	if want := []string{"zfs destroy " + configuredJail + "/etc@auto_2"}; !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran %q, want %q (alone, not -r)", system.ran, want)
	}
}

// The root's own snapshot still goes the way it always did.
func TestTheRootRouteStillDestroysTheRootsSnapshot(t *testing.T) {
	system := withChildSnapshot()
	if _, err := NewJailSource(NewService(system, &fakeLogger{}, "tank")).
		DestroySnapshot(context.Background(), jailMountpoint, "", "auto_1", configuredJail+"@auto_1", true); err != nil {
		t.Fatalf("DestroySnapshot() error = %v", err)
	}
	if want := []string{"zfs destroy -r " + configuredJail + "@auto_1"}; !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran %q, want %q", system.ran, want)
	}
}

// A confirm outside the jail, deeper than a direct child, or for another
// snapshot name is not taken as a child's: the root route refuses it.
func TestTheRootRouteRefusesAConfirmItCannotPlace(t *testing.T) {
	for _, confirm := range []string{
		"tank/jails/containers/other/etc@auto_2",
		configuredJail + "/etc/deeper@auto_2",
		configuredJail + "/etc@auto_1",
	} {
		system := withChildSnapshot()
		if _, err := NewJailSource(NewService(system, &fakeLogger{}, "tank")).
			DestroySnapshot(context.Background(), jailMountpoint, "", "auto_2", confirm, true); err == nil {
			t.Errorf("confirm %q: no error", confirm)
		}
		if len(system.ran) != 0 {
			t.Errorf("confirm %q: ran %q", confirm, system.ran)
		}
	}
}
