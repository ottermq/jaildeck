package storage

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const jailPath = "/usr/local/jails/containers/db1"
const jailRoot = "tank/jails/containers/db1"

// A host with one jail on it, cloned from a template as JD-007 says every
// jail is.
func withAJail() *stubSystem {
	system := withContainersParent()
	system.datasets = append(system.datasets, Dataset{Name: jailRoot, Mountpoint: jailPath})
	system.origins = map[string]string{jailRoot: createTemplate}
	return system
}

func TestCheckDestroyOnAPlainJail(t *testing.T) {
	system := withAJail()

	got, err := jailSourceFor(system, &fakeLogger{}).CheckDestroy(context.Background(), jailPath)
	if err != nil {
		t.Fatalf("CheckDestroy: %v", err)
	}
	if got.Root != jailRoot {
		t.Errorf("Root = %q, want %q", got.Root, jailRoot)
	}
	if got.Blocking() {
		t.Errorf("a jail with nothing of its own blocks a destroy: %+v", got)
	}
	// Nothing was written. This is asked before the jail is stopped.
	if len(system.ran) != 0 {
		t.Errorf("CheckDestroy ran commands: %v", system.ran)
	}
}

func TestCheckDestroyNamesTheChildren(t *testing.T) {
	system := withAJail()
	system.datasets = append(system.datasets,
		Dataset{Name: jailRoot + "/data", Mountpoint: jailPath + "/data"},
		Dataset{Name: jailRoot + "/logs", Mountpoint: jailPath + "/logs"},
		// A jail whose name is a prefix of this one's. It is not a child of
		// it, and a prefix match without the separator would say it was.
		Dataset{Name: jailRoot + "2", Mountpoint: jailPath + "2"},
	)

	got, err := jailSourceFor(system, &fakeLogger{}).CheckDestroy(context.Background(), jailPath)
	if err != nil {
		t.Fatalf("CheckDestroy: %v", err)
	}
	want := []string{jailRoot + "/data", jailRoot + "/logs"}
	if !slices.Equal(got.Children, want) {
		t.Errorf("Children = %v, want %v", got.Children, want)
	}
	// Children are taken by the destroy, not a reason to refuse it (JD-007).
	if got.Blocking() {
		t.Errorf("children blocked the destroy: %+v", got)
	}
}

func TestCheckDestroyReportsWhatWasKeptOnPurpose(t *testing.T) {
	system := withAJail()
	system.datasets = append(system.datasets, Dataset{Name: jailRoot + "/data", Mountpoint: jailPath + "/data"})
	system.snapshots = append(system.snapshots,
		Snapshot{Name: jailRoot + "@base", Dataset: jailRoot, Short: "base", CreateTXG: 20},
		Snapshot{Name: jailRoot + "/data@nightly", Dataset: jailRoot + "/data", Short: "nightly", CreateTXG: 21},
	)
	system.clones = map[string][]string{jailRoot + "@base": {"tank/jails/templates/db"}}
	system.holds = []Hold{{Snapshot: jailRoot + "/data@nightly", Tag: "backup"}}

	got, err := jailSourceFor(system, &fakeLogger{}).CheckDestroy(context.Background(), jailPath)
	if err != nil {
		t.Fatalf("CheckDestroy: %v", err)
	}
	if !got.Blocking() {
		t.Fatalf("want the destroy blocked, got %+v", got)
	}
	// A child's snapshot counts: zfs destroy -r would take it too.
	want := []string{jailRoot + "@base", jailRoot + "/data@nightly"}
	if !slices.Equal(got.Snapshots, want) {
		t.Errorf("Snapshots = %v, want %v", got.Snapshots, want)
	}
	if !slices.Equal(got.Clones, []string{"tank/jails/templates/db"}) {
		t.Errorf("Clones = %v, want the template cloned from it", got.Clones)
	}
	if !slices.Equal(got.Holds, []string{jailRoot + "/data@nightly (backup)"}) {
		t.Errorf("Holds = %v, want the hold named with its tag", got.Holds)
	}
}

func TestCheckDestroyOnAJailThatIsNotOnZFS(t *testing.T) {
	system := withAJail()

	_, err := jailSourceFor(system, &fakeLogger{}).CheckDestroy(context.Background(), "/somewhere/else")
	if err == nil {
		t.Fatal("want an error for a path with no dataset mounted on it")
	}
}

// A jail with no children is destroyed without -r, so that a snapshot taken
// between the check and the destroy makes zfs refuse rather than being taken
// along silently.
func TestDestroyRootWithoutChildrenPassesNoFlag(t *testing.T) {
	system := withAJail()
	logger := &fakeLogger{}

	got, err := jailSourceFor(system, logger).DestroyRoot(context.Background(), jailPath)
	if err != nil {
		t.Fatalf("DestroyRoot: %v", err)
	}
	if got != jailRoot {
		t.Errorf("destroyed %q, want %q", got, jailRoot)
	}
	if !slices.Contains(system.ran, "zfs destroy "+jailRoot) {
		t.Errorf("commands run = %v, want a bare destroy", system.ran)
	}
	for _, command := range system.ran {
		if strings.Contains(command, "-R") {
			t.Errorf("-R was passed: %q -- it would take dependent clones", command)
		}
	}
	if len(logger.entries) == 0 {
		t.Fatal("the destroy was not audited")
	}
	last := logger.entries[len(logger.entries)-1]
	if last.Operation != OperationDestroyDataset || last.Target != jailRoot || !last.Success {
		t.Errorf("audit entry = %+v", last)
	}
}

func TestDestroyRootWithChildrenPassesMinusR(t *testing.T) {
	system := withAJail()
	system.datasets = append(system.datasets, Dataset{Name: jailRoot + "/data", Mountpoint: jailPath + "/data"})

	if _, err := jailSourceFor(system, &fakeLogger{}).DestroyRoot(context.Background(), jailPath); err != nil {
		t.Fatalf("DestroyRoot: %v", err)
	}
	if !slices.Contains(system.ran, "zfs destroy -r "+jailRoot) {
		t.Errorf("commands run = %v, want -r so the children go with it", system.ran)
	}
	// The children went with it, which is the point of the flag.
	for _, dataset := range system.datasets {
		if strings.HasPrefix(dataset.Name, jailRoot) {
			t.Errorf("%s survived the destroy", dataset.Name)
		}
	}
}

// A snapshot created after CheckDestroy said there were none. zfs refuses the
// bare destroy and the refusal reaches the caller -- which is the whole
// reason -r is not passed unconditionally.
func TestDestroyRootLetsZFSRefuseASnapshotTakenInTheGap(t *testing.T) {
	system := withAJail()
	system.snapshots = append(system.snapshots,
		Snapshot{Name: jailRoot + "@late", Dataset: jailRoot, Short: "late", CreateTXG: 30})

	_, err := jailSourceFor(system, &fakeLogger{}).DestroyRoot(context.Background(), jailPath)
	if err == nil {
		t.Fatal("want zfs's refusal, got none")
	}
	if !strings.Contains(err.Error(), "filesystem has children") {
		t.Errorf("want zfs's own message in the error, got %q", err)
	}
	if !slices.ContainsFunc(system.datasets, func(d Dataset) bool { return d.Name == jailRoot }) {
		t.Error("the root was destroyed despite the refusal")
	}
}

// The busy refusal JD-030 leans on: nothing here checks that the jail is
// down, so zfs's answer has to reach the caller intact.
func TestDestroyRootPassesOnABusyDataset(t *testing.T) {
	system := withAJail()
	system.writeErr = errors.New("cannot destroy '" + jailRoot + "': pool or dataset is busy")
	logger := &fakeLogger{}

	_, err := jailSourceFor(system, logger).DestroyRoot(context.Background(), jailPath)
	if err == nil {
		t.Fatal("want the busy refusal")
	}
	if !strings.Contains(err.Error(), "pool or dataset is busy") {
		t.Errorf("want zfs's own message, got %q", err)
	}
	if len(logger.entries) == 0 || logger.entries[len(logger.entries)-1].Success {
		t.Errorf("a failed destroy was not audited as a failure: %+v", logger.entries)
	}
}
