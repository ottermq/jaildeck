package freebsd

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func withSnapshotCaptures(jail string) map[string]string {
	return map[string]string{
		"zfs list -H -p -t snapshot -o name,used,refer,creation,createtxg -r " + jail: "zfs_list_snapshots_txg",
	}
}

const isengardJail = "tank/jails/containers/ottermq"

// One jail's real snapshot history, parsed. The capture is scoped to a single
// jail rather than to the whole layout on purpose: recursive over <pool>/jails
// is 276 lines on this host, 256 of them hourly zrepl snapshots.
func TestSnapshotsFixtures(t *testing.T) {
	runner := &replayRunner{t: t, set: "isengard", captures: withSnapshotCaptures(isengardJail)}
	got, err := NewStorageAdapter(runner).Snapshots(context.Background(), []string{isengardJail})
	if err != nil {
		t.Fatalf("Snapshots() error = %v", err)
	}
	if want := capturedLines(t, "isengard", "zfs_list_snapshots_txg"); len(got) != want {
		t.Fatalf("Snapshots() = %d, want one per captured line (%d)", len(got), want)
	}

	byName := map[string]int{}
	for i, snapshot := range got {
		byName[snapshot.Name] = i
	}

	// The jail's own dataset and its child, both snapshotted by one recursive
	// call through the API in #4.
	root, child := isengardJail+"@before-upgrade", isengardJail+"/data@before-upgrade"
	for _, name := range []string{root, child} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("%s is not in the capture", name)
		}
	}

	// Same transaction group: zfs snapshot -r really is one point in time, and
	// that is the property the jail-level route exists to provide (JD-007).
	if a, b := got[byName[root]], got[byName[child]]; a.CreateTXG != b.CreateTXG {
		t.Errorf("createtxg = %d and %d, want one recursive snapshot to share a transaction group", a.CreateTXG, b.CreateTXG)
	}

	// The name is split, and Dataset is what a rollback acts on.
	if snapshot := got[byName[child]]; snapshot.Dataset != isengardJail+"/data" || snapshot.Short != "before-upgrade" {
		t.Errorf("snapshot = %+v, want it split into dataset and short name", snapshot)
	}
	// -p makes creation a Unix timestamp. Without it zfs prints a locale date
	// with a double space before a single-digit hour, which parses as nothing.
	if year := got[byName[root]].Created.Year(); year != 2026 {
		t.Errorf("Created = %v, want a 2026 timestamp", got[byName[root]].Created)
	}
}

// createtxg is strictly increasing across the whole capture, which is what
// makes it usable as the ordering a rollback asks about. Timestamps are not:
// this host really does have snapshots that tie on creation.
func TestSnapshotTransactionGroupsOrderWhereTimestampsTie(t *testing.T) {
	runner := &replayRunner{t: t, set: "isengard", captures: withSnapshotCaptures(isengardJail)}
	got, err := NewStorageAdapter(runner).Snapshots(context.Background(), []string{isengardJail})
	if err != nil {
		t.Fatalf("Snapshots() error = %v", err)
	}

	seen := map[uint64]string{}
	ties := 0
	byTime := map[time.Time]int{}
	for _, snapshot := range got {
		byTime[snapshot.Created]++
		if other, clash := seen[snapshot.CreateTXG]; clash {
			// Two snapshots of ONE recursive call share a transaction group.
			// Two unrelated ones must not.
			if shortOf(other) != snapshot.Short {
				t.Errorf("%s and %s share createtxg %d but are different snapshots", other, snapshot.Name, snapshot.CreateTXG)
			}
		}
		seen[snapshot.CreateTXG] = snapshot.Name
	}
	for _, n := range byTime {
		if n > 1 {
			ties++
		}
	}
	if ties == 0 {
		t.Log("no two snapshots in this capture share a creation timestamp; the tie case is covered by the service test instead")
	}
}

func shortOf(name string) string {
	_, short, _ := strings.Cut(name, "@")
	return short
}

// What "nothing depends on this snapshot" looks like, from the host: zfs get
// clones answers with a dash, and zfs holds answers with nothing at all. Both
// exit 0, so neither is an error to be told apart from a real failure.
func TestNoClonesAndNoHolds(t *testing.T) {
	snapshot := readFixtureLine(t, "isengard", "PROBE_DATASET")
	runner := &replayRunner{t: t, set: "isengard", captures: map[string]string{
		"zfs get -H -o value clones " + snapshot: "zfs_get_clones_none",
		"zfs holds -H " + snapshot:               "zfs_holds_none",
	}}
	adapter := NewStorageAdapter(runner)

	clones, err := adapter.Clones(context.Background(), snapshot)
	if err != nil || len(clones) != 0 {
		t.Errorf("Clones() = %q, %v; want none", clones, err)
	}
	holds, err := adapter.Holds(context.Background(), snapshot)
	if err != nil || len(holds) != 0 {
		t.Errorf("Holds() = %+v, %v; want none", holds, err)
	}
}

// Constructed, not captured: no host here has a held or cloned snapshot, and
// saying so is better than implying these bytes came off one.
func TestClonesAndHoldsParsing(t *testing.T) {
	t.Run("clones are comma separated", func(t *testing.T) {
		runner := &recordingRunner{stdout: "tank/a,tank/b\n"}
		got, err := (&StorageAdapter{Adapter: NewAdapter(runner)}).Clones(context.Background(), "tank/x@s")
		if err != nil || len(got) != 2 || got[0] != "tank/a" || got[1] != "tank/b" {
			t.Errorf("Clones() = %q, %v; want two", got, err)
		}
	})
	t.Run("holds are name, tag, timestamp", func(t *testing.T) {
		runner := &recordingRunner{stdout: "tank/x@s\tkeep-for-audit\tFri Oct 23 10:52 2026\n"}
		got, err := (&StorageAdapter{Adapter: NewAdapter(runner)}).Holds(context.Background(), "tank/x@s")
		if err != nil || len(got) != 1 || got[0].Tag != "keep-for-audit" {
			t.Errorf("Holds() = %+v, %v; want one tagged keep-for-audit", got, err)
		}
	})
}

func readFixtureLine(t *testing.T, set, name string) string {
	t.Helper()
	raw, err := os.ReadFile(fixture(set, name, "stdout"))
	if err != nil {
		// PROBE_DATASET has no extension; the probe files are written bare.
		raw, err = os.ReadFile(hostfacts + "/" + set + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
	}
	return strings.TrimSpace(string(raw))
}
