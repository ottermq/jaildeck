package storage

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func withRetainable() *stubSystem {
	return &stubSystem{
		pools: []string{"tank"},
		datasets: []Dataset{
			{Name: "tank/jails", Mountpoint: "/usr/local/jails"},
			{Name: "tank/jails/containers/ottermq", Mountpoint: "/usr/local/jails/containers/ottermq"},
			{Name: "tank/jails/containers/ottermq/data", Mountpoint: "/usr/local/jails/containers/ottermq/data"},
		},
		clones:     map[string][]string{},
		notMounted: map[string]bool{},
	}
}

const ottermqData = "tank/jails/containers/ottermq/data"

// Three commands, in this order, and nothing else. The rename carries -u so a
// running jail keeps its filesystem; the unmount and the mount are what
// actually move it, at a moment this chose rather than one zfs chose.
func TestRetainMovesAndRemounts(t *testing.T) {
	system := withRetainable()
	logger := &fakeLogger{}

	moved, err := NewService(system, logger, "tank").
		RetainDataset(context.Background(), ottermqData, "ottermq-data")
	if err != nil {
		t.Fatalf("RetainDataset() error = %v", err)
	}

	want := []string{
		"zfs create tank/jails/retained",
		"zfs rename -u " + ottermqData + " tank/jails/retained/ottermq-data",
		"zfs unmount tank/jails/retained/ottermq-data",
		"zfs mount tank/jails/retained/ottermq-data",
	}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran\n  %q\nwant\n  %q", system.ran, want)
	}
	if moved.To != "tank/jails/retained/ottermq-data" || !moved.Remounted {
		t.Errorf("moved = %+v, want it carried to retained and remounted", moved)
	}
	// Four commands, four audit lines: an operator asking what happened to
	// this dataset gets the whole sequence, not a summary of it.
	if len(logger.entries) != 4 {
		t.Errorf("wrote %d audit entries, want one per command: %+v", len(logger.entries), logger.entries)
	}
}

// The retained parent is made the first time one is needed and not again.
func TestRetainedParentIsCreatedOnce(t *testing.T) {
	system := withRetainable()
	system.datasets = append(system.datasets,
		Dataset{Name: "tank/jails/retained", Mountpoint: "/usr/local/jails/retained"})

	if _, err := NewService(system, &fakeLogger{}, "tank").
		RetainDataset(context.Background(), ottermqData, "ottermq-data"); err != nil {
		t.Fatalf("RetainDataset() error = %v", err)
	}
	for _, command := range system.ran {
		if command == "zfs create tank/jails/retained" {
			t.Error("created tank/jails/retained when it already existed")
		}
	}
}

// A dataset that is not mounted has nothing to carry, and mounting it would be
// a change nobody asked for.
func TestRetainDoesNotMountSomethingThatWasNot(t *testing.T) {
	system := withRetainable()
	system.notMounted[ottermqData] = true

	moved, err := NewService(system, &fakeLogger{}, "tank").
		RetainDataset(context.Background(), ottermqData, "ottermq-data")
	if err != nil {
		t.Fatalf("RetainDataset() error = %v", err)
	}
	if moved.Remounted {
		t.Error("Remounted = true for a dataset that was not mounted")
	}
	for _, command := range system.ran {
		if command == "zfs unmount tank/jails/retained/ottermq-data" ||
			command == "zfs mount tank/jails/retained/ottermq-data" {
			t.Errorf("ran %q for a dataset that was not mounted", command)
		}
	}
}

func TestMoveRefusals(t *testing.T) {
	tests := []struct {
		name     string
		arrange  func(*stubSystem)
		source   string
		retained string
		wantCode int
	}{
		{
			name:     "the target is taken",
			arrange:  func(s *stubSystem) { s.datasets = append(s.datasets, Dataset{Name: "tank/jails/retained/taken"}) },
			source:   ottermqData,
			retained: "taken",
			wantCode: http.StatusConflict,
		},
		{
			name:     "no such source",
			source:   "tank/jails/containers/ottermq/nope",
			retained: "x",
			wantCode: http.StatusNotFound,
		},
		{
			name:     "not a dataset name",
			source:   ottermqData,
			retained: "../escape",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "a slash would make it a nested dataset",
			source:   ottermqData,
			retained: "a/b",
			wantCode: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withRetainable()
			if tt.arrange != nil {
				tt.arrange(system)
			}
			_, err := NewService(system, &fakeLogger{}, "tank").
				RetainDataset(context.Background(), tt.source, tt.retained)
			if err == nil {
				t.Fatal("RetainDataset() returned no error")
			}
			if code := appErrCode(t, err); code != tt.wantCode {
				t.Errorf("error code = %d, want %d (%v)", code, tt.wantCode, err)
			}
			for _, command := range system.ran {
				if strings.HasPrefix(command, "zfs rename") {
					t.Errorf("ran %q despite refusing", command)
				}
			}
		})
	}
}

// The round trip the issue asks for, without the jail: out, and back in under
// a different name than it left.
func TestRetainThenRestore(t *testing.T) {
	system := withRetainable()
	service := NewService(system, &fakeLogger{}, "tank")

	if _, err := service.RetainDataset(context.Background(), ottermqData, "kept"); err != nil {
		t.Fatalf("RetainDataset() error = %v", err)
	}
	retained, err := service.RetainedDatasets(context.Background())
	if err != nil {
		t.Fatalf("RetainedDatasets() error = %v", err)
	}
	if len(retained) != 1 || retained[0].Name != "tank/jails/retained/kept" {
		t.Fatalf("retained = %+v, want the one dataset", retained)
	}

	const newHome = "tank/jails/containers/ottermq-2/data"
	system.datasets = append(system.datasets,
		Dataset{Name: "tank/jails/containers/ottermq-2", Mountpoint: "/usr/local/jails/containers/ottermq-2"})

	moved, err := service.RestoreDataset(context.Background(), "tank", "kept", newHome)
	if err != nil {
		t.Fatalf("RestoreDataset() error = %v", err)
	}
	if moved.From != "tank/jails/retained/kept" || moved.To != newHome {
		t.Errorf("moved = %+v, want kept -> %s", moved, newHome)
	}
	// And nothing is retained any more.
	if retained, _ := service.RetainedDatasets(context.Background()); len(retained) != 0 {
		t.Errorf("retained = %+v, want none after the restore", retained)
	}
}

// A failed rename must not leave the caller thinking the dataset moved, and
// the attempt is still audited.
func TestFailedRenameIsReportedAndAudited(t *testing.T) {
	system := withRetainable()
	// The retained parent already exists, so the rename is the first write
	// that runs and the first one to fail.
	system.datasets = append(system.datasets,
		Dataset{Name: "tank/jails/retained", Mountpoint: "/usr/local/jails/retained"})
	system.writeErr = errors.New("cannot rename: permission denied")
	logger := &fakeLogger{}

	if _, err := NewService(system, logger, "tank").
		RetainDataset(context.Background(), ottermqData, "x"); err == nil {
		t.Fatal("RetainDataset() returned no error")
	}
	failed := false
	for _, entry := range logger.entries {
		if entry.Operation == OperationRenameDataset && !entry.Success {
			failed = true
		}
	}
	if !failed {
		t.Errorf("audit = %+v, want a failed rename-dataset entry", logger.entries)
	}
}
