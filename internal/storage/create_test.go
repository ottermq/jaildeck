package storage

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

const createTemplate = "tank/jails/templates/15.1-RELEASE@base"

// A host as JD-021 lays one out, with a ready template and the containers
// parent already in place.
func withContainersParent() *stubSystem {
	return &stubSystem{
		pools: []string{"tank"},
		datasets: []Dataset{
			{Name: "tank/jails", Mountpoint: "/usr/local/jails"},
			{Name: "tank/jails/containers", Mountpoint: "/usr/local/jails/containers"},
			{Name: "tank/jails/templates/15.1-RELEASE", Mountpoint: "/usr/local/jails/templates/15.1-RELEASE"},
		},
		snapshots: []Snapshot{
			{Name: createTemplate, Dataset: "tank/jails/templates/15.1-RELEASE", Short: "base", CreateTXG: 10},
		},
		origins:    map[string]string{},
		clones:     map[string][]string{},
		notMounted: map[string]bool{},
	}
}

func jailSourceFor(system *stubSystem, logger *fakeLogger) *JailSource {
	return NewJailSource(NewService(system, logger, "tank"))
}

func TestCloneRoot(t *testing.T) {
	system := withContainersParent()
	logger := &fakeLogger{}

	got, err := jailSourceFor(system, logger).
		CloneRoot(context.Background(), createTemplate, "db1")
	if err != nil {
		t.Fatalf("CloneRoot: %v", err)
	}

	// The layout JD-021 fixes, in the template's own pool.
	if got.Dataset != "tank/jails/containers/db1" {
		t.Errorf("dataset = %q, want tank/jails/containers/db1", got.Dataset)
	}
	// Read back from ZFS rather than composed, so a pool that mounts the
	// layout somewhere else still reports the jail's real path.
	if got.Mountpoint != "/usr/local/jails/containers/db1" {
		t.Errorf("mountpoint = %q, want the path the clone inherited", got.Mountpoint)
	}
	if !slices.Contains(system.ran, "zfs clone "+createTemplate+" tank/jails/containers/db1") {
		t.Errorf("commands run = %v, want the clone", system.ran)
	}
	// The containers parent was already there, so nothing created anything.
	for _, command := range system.ran {
		if strings.HasPrefix(command, "zfs create ") {
			t.Errorf("created a dataset that already existed: %q", command)
		}
	}
	if len(logger.entries) == 0 || logger.entries[len(logger.entries)-1].Operation != OperationCloneDataset {
		t.Errorf("audit entries = %+v, want the clone logged", logger.entries)
	}
}

// A host that has templates but has never made a jail. Telling the operator to
// go and run `jaildeck init` first would be a worse answer than creating the
// one dataset that is missing.
func TestCloneRootCreatesTheContainersParent(t *testing.T) {
	system := withContainersParent()
	system.datasets = slices.DeleteFunc(system.datasets, func(d Dataset) bool {
		return d.Name == "tank/jails/containers"
	})

	got, err := jailSourceFor(system, &fakeLogger{}).
		CloneRoot(context.Background(), createTemplate, "db1")
	if err != nil {
		t.Fatalf("CloneRoot: %v", err)
	}
	if got.Dataset != "tank/jails/containers/db1" {
		t.Errorf("dataset = %q, want it under the parent that was just created", got.Dataset)
	}
	created := slices.IndexFunc(system.ran, func(c string) bool {
		return strings.HasPrefix(c, "zfs create tank/jails/containers")
	})
	cloned := slices.IndexFunc(system.ran, func(c string) bool {
		return strings.HasPrefix(c, "zfs clone ")
	})
	if created < 0 {
		t.Fatalf("commands run = %v, want the parent created", system.ran)
	}
	if cloned < created {
		t.Errorf("cloned before creating the parent: %v", system.ran)
	}
}

// zfs refuses a dataset that is already there, with its own message. Nothing
// here checks first: a check answers a question that can change before the
// clone runs.
func TestCloneRootPassesZFSRefusalBack(t *testing.T) {
	system := withContainersParent()
	system.cloneErr = errors.New("cannot create 'tank/jails/containers/db1': dataset already exists")

	_, err := jailSourceFor(system, &fakeLogger{}).
		CloneRoot(context.Background(), createTemplate, "db1")
	if err == nil {
		t.Fatal("CloneRoot over an existing dataset = nil error, want zfs's refusal")
	}
	if !strings.Contains(err.Error(), "dataset already exists") {
		t.Errorf("error = %q, want zfs's own message", err)
	}
}

func TestCloneRootRefusesASnapshotOutsideAPool(t *testing.T) {
	_, err := jailSourceFor(withContainersParent(), &fakeLogger{}).
		CloneRoot(context.Background(), "notapool@base", "db1")
	if err == nil {
		t.Fatal("CloneRoot from a poolless snapshot = nil error, want one")
	}
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusBadRequest {
		t.Errorf("error = %v, want a 400", err)
	}
}

func TestCheckTemplate(t *testing.T) {
	tests := []struct {
		name     string
		snapshot string
		wantCode int
		wantText string
	}{
		{name: "a ready template", snapshot: createTemplate},
		{
			name:     "not a snapshot name",
			snapshot: "tank/jails/templates/15.1-RELEASE",
			wantCode: http.StatusBadRequest,
			wantText: "has to be dataset@snapshot",
		},
		{
			name:     "no such snapshot",
			snapshot: "tank/jails/templates/15.1-RELEASE@p9",
			wantCode: http.StatusNotFound,
			wantText: "no snapshot named",
		},
		{
			name:     "no such dataset",
			snapshot: "tank/jails/templates/14.2-RELEASE@base",
			wantCode: http.StatusNotFound,
			wantText: "no ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withContainersParent()
			err := jailSourceFor(system, &fakeLogger{}).
				CheckTemplate(context.Background(), tt.snapshot)
			if tt.wantCode == 0 {
				if err != nil {
					t.Fatalf("CheckTemplate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("CheckTemplate = nil error, want a refusal")
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantText)
			}
			// Nothing is created on the way to refusing.
			if len(system.ran) != 0 {
				t.Errorf("a refused check ran %v", system.ran)
			}
		})
	}
}
