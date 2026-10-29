package storage

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

// #121: a child whose mountpoint is set on itself, inside its jail -- a
// database at /var/db/postgres. Measured on isengard: retained the JD-025 way,
// it stays mounted there, and the jail's destroy fails "pool or dataset is busy".
const ottermqPG = "tank/jails/containers/ottermq/pg"

func withLocalMountpoint() *stubSystem {
	system := withRetainable()
	system.datasets = append(system.datasets,
		Dataset{Name: "tank/jails/retained", Mountpoint: "/usr/local/jails/retained"},
		Dataset{Name: ottermqPG, Mountpoint: "/usr/local/jails/containers/ottermq/var/db/postgres"})
	system.properties = []Property{{Dataset: ottermqPG, Name: "mountpoint",
		Value: "/usr/local/jails/containers/ottermq/var/db/postgres"}}
	return system
}

func TestRetainRecordsALocalMountpointAndInheritsIt(t *testing.T) {
	system := withLocalMountpoint()
	logger := &fakeLogger{}
	moved, err := NewService(system, logger, "tank").RetainDataset(context.Background(), ottermqPG, "ottermq-pg")
	if err != nil {
		t.Fatal(err)
	}
	// No zfs mount: the inherit mounted it under retained, as zfs does.
	want := []string{
		"zfs rename -u " + ottermqPG + " tank/jails/retained/ottermq-pg",
		"zfs unmount tank/jails/retained/ottermq-pg",
		"zfs set jaildeck:mountpoint=var/db/postgres tank/jails/retained/ottermq-pg",
		"zfs inherit mountpoint tank/jails/retained/ottermq-pg",
	}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran\n  %q\nwant\n  %q", system.ran, want)
	}
	if !moved.Remounted || moved.Mountpoint != "/usr/local/jails/retained/ottermq-pg" {
		t.Errorf("moved = %+v, want it remounted under retained", moved)
	}
	if mounted, _ := system.Mounted(context.Background(), "tank/jails/retained/ottermq-pg"); !mounted {
		t.Error("the retained dataset was left unmounted")
	}
	if len(logger.entries) != len(want) {
		t.Errorf("audited %d steps, want %d", len(logger.entries), len(want))
	}
}

// The round trip, into a jail of another name: the path is rebuilt under that
// jail's, and the record is gone.
func TestRestorePutsTheMountpointBackUnderTheNewJail(t *testing.T) {
	system := withLocalMountpoint()
	service := NewService(system, &fakeLogger{}, "tank")
	if _, err := service.RetainDataset(context.Background(), ottermqPG, "ottermq-pg"); err != nil {
		t.Fatal(err)
	}
	system.datasets = append(system.datasets,
		Dataset{Name: "tank/jails/containers/db2", Mountpoint: "/usr/local/jails/containers/db2"})
	system.ran = nil

	moved, err := service.RestoreDataset(context.Background(), "tank", "ottermq-pg", "tank/jails/containers/db2/pg")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"zfs rename -u tank/jails/retained/ottermq-pg tank/jails/containers/db2/pg",
		"zfs unmount tank/jails/containers/db2/pg",
		"zfs set mountpoint=/usr/local/jails/containers/db2/var/db/postgres tank/jails/containers/db2/pg",
		"zfs inherit jaildeck:mountpoint tank/jails/containers/db2/pg",
	}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran\n  %q\nwant\n  %q", system.ran, want)
	}
	if moved.Mountpoint != "/usr/local/jails/containers/db2/var/db/postgres" || !moved.Remounted {
		t.Errorf("moved = %+v", moved)
	}
	if v := system.localValue("tank/jails/containers/db2/pg", MountpointProperty); v != "" {
		t.Errorf("%s is still %q after the restore", MountpointProperty, v)
	}
}

// A dataset that was not mounted is left not mounted, though setting and
// inheriting a mountpoint mount it on the way.
func TestALocalMountpointThatWasNotMountedStaysNotMounted(t *testing.T) {
	system := withLocalMountpoint()
	system.notMounted[ottermqPG] = true
	moved, err := NewService(system, &fakeLogger{}, "tank").RetainDataset(context.Background(), ottermqPG, "ottermq-pg")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"zfs rename -u " + ottermqPG + " tank/jails/retained/ottermq-pg",
		"zfs set jaildeck:mountpoint=var/db/postgres tank/jails/retained/ottermq-pg",
		"zfs inherit mountpoint tank/jails/retained/ottermq-pg",
		"zfs unmount tank/jails/retained/ottermq-pg",
	}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran\n  %q\nwant\n  %q", system.ran, want)
	}
	if moved.Remounted {
		t.Error("Remounted for a dataset that was not mounted")
	}
	if mounted, _ := system.Mounted(context.Background(), "tank/jails/retained/ottermq-pg"); mounted {
		t.Error("left mounted")
	}
}

func TestMountpointRefusals(t *testing.T) {
	conflict := func(t *testing.T, err error, says string) {
		t.Helper()
		var appErr *common.AppError
		if !errors.As(err, &appErr) || appErr.Code != http.StatusConflict || !strings.Contains(err.Error(), says) {
			t.Errorf("err = %v, want a 409 saying %q", err, says)
		}
	}

	t.Run("outside the jail, before anything runs", func(t *testing.T) {
		system := withRetainable()
		system.properties = []Property{{Dataset: ottermqData, Name: "mountpoint", Value: "/srv/data"}}
		_, err := NewService(system, &fakeLogger{}, "tank").RetainDataset(context.Background(), ottermqData, "kept")
		conflict(t, err, "/srv/data, which is not inside its jail")
		if len(system.ran) != 0 {
			t.Errorf("ran %q", system.ran)
		}
	})

	t.Run("the jail's own path", func(t *testing.T) {
		system := withRetainable()
		system.properties = []Property{{Dataset: ottermqData, Name: "mountpoint", Value: "/usr/local/jails/containers/ottermq"}}
		_, err := NewService(system, &fakeLogger{}, "tank").RetainDataset(context.Background(), ottermqData, "kept")
		conflict(t, err, "not inside its jail")
	})

	t.Run("a record that climbs out of the new jail", func(t *testing.T) {
		system := withRetainable()
		system.datasets = append(system.datasets,
			Dataset{Name: "tank/jails/retained", Mountpoint: "/usr/local/jails/retained"},
			Dataset{Name: "tank/jails/retained/kept", Mountpoint: "/usr/local/jails/retained/kept"})
		system.properties = []Property{{Dataset: "tank/jails/retained/kept", Name: MountpointProperty, Value: "../../etc"}}
		_, err := NewService(system, &fakeLogger{}, "tank").
			RestoreDataset(context.Background(), "tank", "kept", "tank/jails/containers/ottermq/kept")
		conflict(t, err, "not a path inside a jail")
		if len(system.ran) != 0 {
			t.Errorf("ran %q", system.ran)
		}
	})
}
