package freebsd

import (
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/storage"
)

// withStorageRoots adds the two storage captures a set has for each root, keyed
// by the command line capture.sh ran. A command this map has no key for is an
// adapter that stopped asking what the fixtures answer, and replayRunner fails
// the test by name rather than quietly returning nothing.
func withStorageRoots(roots ...string) map[string]string {
	captures := make(map[string]string, 2*len(roots))
	for _, root := range roots {
		captures["zfs list -H -p -o name,used,avail,refer,mountpoint -r "+root] = "zfs_list_space_rp_" + root
		captures["zfs get -H -r -o name,property,value,source -s local all "+root] = "zfs_get_local_r_" + root
	}
	return captures
}

// The byte figures below are a live host's, so a re-capture moves them and this
// test fails loudly with the new value in the message. That is the intended
// cost: asserting the whole struct is what proves all five columns land in the
// right fields, which a weaker check would not.
func TestDatasetsUnderFixtures(t *testing.T) {
	tests := []struct {
		set, root string
		count     int
		want      storage.Dataset // one line the parse must have produced
	}{
		// A clone's referenced space exceeds its used space, because it still
		// shares the template's blocks -- the reason JD-007 puts a quota on a
		// child dataset and not on a jail's root. These are those bytes.
		{"isengard", "tank", 9, storage.Dataset{
			Name: "tank/jails/containers/postgres", Mountpoint: "/usr/local/jails/containers/postgres",
			Used: 1519112192, Available: 963192627200, Referenced: 1891311616}},
		// A pool root mounted nowhere, which must never be matched to a jail.
		{"isengard", "tank", 9, storage.Dataset{
			Name: "tank", Mountpoint: "none",
			Used: 2369748992, Available: 963192627200, Referenced: 98304}},
		// The replication target: 23 datasets, every one of them mounted
		// nowhere (JD-021).
		{"isengard", "backup", 23, storage.Dataset{
			Name: "backup/isengard/tank/jails", Mountpoint: "none",
			Used: 3499880448, Available: 476441370624, Referenced: 164872192}},
		{"rohan", "media", 7, storage.Dataset{
			Name: "media/.incoming", Mountpoint: "/srv/media/.incoming",
			Used: 1517826048, Available: 783248302080, Referenced: 1517826048}},
		{"vm-15.1", "zroot", 21, storage.Dataset{
			Name: "zroot/jails", Mountpoint: "/usr/local/jails",
			Used: 994848768, Available: 33793187840, Referenced: 114688}},
	}
	for _, tt := range tests {
		t.Run(tt.set+"/"+tt.want.Name, func(t *testing.T) {
			runner := &replayRunner{t: t, set: tt.set, captures: withStorageRoots(tt.root)}
			got, err := NewAdapter(runner).DatasetsUnder(context.Background(), []string{tt.root})
			if err != nil {
				t.Fatalf("DatasetsUnder() error = %v", err)
			}
			if len(got) != tt.count {
				t.Errorf("DatasetsUnder() = %d datasets, want %d", len(got), tt.count)
			}
			if !slices.Contains(got, tt.want) {
				t.Errorf("DatasetsUnder() did not produce %+v", tt.want)
			}
		})
	}

	t.Run("several roots in one call", func(t *testing.T) {
		// The batched shape #2 requires: every pool of a host, one command.
		runner := &replayRunner{t: t, set: "isengard",
			captures: map[string]string{
				"zfs list -H -p -o name,used,avail,refer,mountpoint -r tank zroot": "zfs_list_space_rp_tank",
			}}
		if _, err := NewAdapter(runner).DatasetsUnder(context.Background(), []string{"tank", "zroot"}); err != nil {
			t.Fatalf("DatasetsUnder() error = %v", err)
		}
		if len(runner.ran) != 1 {
			t.Errorf("ran %d commands for two roots, want 1: %v", len(runner.ran), runner.ran)
		}
	})
}

// Without -p the same query returns "2.20G" and "897G". That is the default,
// it parses as nothing, and the error has to say which flag is missing -- so
// the test feeds the parser the capture of exactly that mistake.
func TestDatasetsUnderRejectsHumanReadableSizes(t *testing.T) {
	human, err := os.ReadFile(fixture("isengard", "zfs_list_space_r_tank", "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseZFSListSpace(string(human))
	if err == nil {
		t.Fatalf("parseZFSListSpace(human-readable) = %+v, nil; want an error", got)
	}
	if !strings.Contains(err.Error(), "-p") {
		t.Errorf("error = %q, want it to name the missing -p", err)
	}
}

func TestLocalPropertiesFixtures(t *testing.T) {
	tests := []struct {
		set, root string
		count     int
		want      storage.Property
	}{
		// Native properties come back too: filtering to user properties is the
		// service's rule, not the adapter's (JD-010).
		{"isengard", "tank", 8, storage.Property{Dataset: "tank/jails", Name: "mountpoint", Value: "/usr/local/jails"}},
		// A native property set locally ON A JAIL'S OWN DATASET, which is the
		// only arrangement that makes the colon filter load-bearing: without
		// one, dropping the filter changes nothing a jail ever sees.
		{"isengard", "tank", 8, storage.Property{Dataset: "tank/jails/containers/ottermq", Name: "atime", Value: "off"}},
		// The tags an Otter Labs client sets, captured from the host after a
		// zfs set rather than invented: this is the evidence #2 exists for.
		{"isengard", "tank", 8, storage.Property{Dataset: "tank/jails/containers/ottermq", Name: "otter:instance", Value: "mq-01"}},
		{"isengard", "tank", 8, storage.Property{Dataset: "tank/jails/containers/postgres", Name: "otter:instance", Value: "db-01"}},
		// The only real user properties on any of these hosts.
		{"isengard", "backup", 29, storage.Property{Dataset: "backup/isengard", Name: "zrepl:placeholder", Value: "on"}},
		{"rohan", "zroot", 32, storage.Property{Dataset: "zroot/srv/builds", Name: "com.sun:auto-snapshot", Value: "false"}},
		{"rohan", "media", 3, storage.Property{Dataset: "media", Name: "compression", Value: "lz4"}},
	}
	for _, tt := range tests {
		t.Run(tt.set+"/"+tt.root, func(t *testing.T) {
			runner := &replayRunner{t: t, set: tt.set, captures: withStorageRoots(tt.root)}
			got, err := NewAdapter(runner).LocalProperties(context.Background(), []string{tt.root})
			if err != nil {
				t.Fatalf("LocalProperties() error = %v", err)
			}
			if len(got) != tt.count {
				t.Errorf("LocalProperties() = %d properties, want %d", len(got), tt.count)
			}
			if !slices.Contains(got, tt.want) {
				t.Errorf("LocalProperties() did not produce %+v", tt.want)
			}
		})
	}
}

// -s local is what makes every row's source "local", so the source column is
// redundant -- and checking it is what makes asking for it worthwhile. The
// capture of the same query WITHOUT -s local is the proof it can fail: its
// rows carry "default" and "inherited from tank/jails".
func TestLocalPropertiesRejectsRowsThatAreNotLocal(t *testing.T) {
	all, err := os.ReadFile(fixture("isengard", "zfs_get_all", "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseZFSGetLocal(string(all))
	if err == nil {
		t.Fatalf("parseZFSGetLocal(every source) = %d properties, nil; want an error", len(got))
	}
	if !strings.Contains(err.Error(), "local") {
		t.Errorf("error = %q, want it to say the source is not local", err)
	}
}

// A user property's value is whatever the operator set, which makes it the one
// column that can hold a tab. Constructed, not captured: no host here has such
// a property, and inventing the bytes is honest where pretending they were
// captured would not be.
func TestLocalPropertiesKeepsATabbedValue(t *testing.T) {
	got, err := parseZFSGetLocal("tank/jails\totter:note\ta\tb\tlocal\n")
	if err != nil {
		t.Fatalf("parseZFSGetLocal() error = %v", err)
	}
	want := storage.Property{Dataset: "tank/jails", Name: "otter:note", Value: "a\tb"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("parseZFSGetLocal() = %+v, want [%+v]", got, want)
	}
}

// The argument prefixes are package-level slices, so a call that appended its
// roots to one would corrupt the next call's command.
func TestStorageCommandsDoNotShareArguments(t *testing.T) {
	runner := &replayRunner{t: t, set: "isengard", captures: withStorageRoots("tank", "zroot")}
	a := NewAdapter(runner)
	for _, root := range []string{"tank", "zroot", "tank"} {
		if _, err := a.DatasetsUnder(context.Background(), []string{root}); err != nil {
			t.Fatalf("DatasetsUnder(%s) error = %v", root, err)
		}
		if _, err := a.LocalProperties(context.Background(), []string{root}); err != nil {
			t.Fatalf("LocalProperties(%s) error = %v", root, err)
		}
	}
	for _, line := range runner.ran {
		if strings.Count(line, "tank")+strings.Count(line, "zroot") != 1 {
			t.Errorf("ran %q, which names more than one root", line)
		}
	}
}

// Every storage capture on every host parses. A new capture that does not is a
// format difference this package has not been told about.
func TestEveryStorageCaptureParses(t *testing.T) {
	sets, err := os.ReadDir(hostfacts)
	if err != nil {
		t.Fatal(err)
	}
	parsed := 0
	for _, set := range sets {
		if !set.IsDir() {
			continue
		}
		entries, err := os.ReadDir(hostfacts + "/" + set.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".stdout")
			if name == e.Name() {
				continue
			}
			raw, err := os.ReadFile(fixture(set.Name(), name, "stdout"))
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case strings.HasPrefix(name, "zfs_list_space_rp_"):
				if _, err := parseZFSListSpace(string(raw)); err != nil {
					t.Errorf("%s/%s: %v", set.Name(), name, err)
				}
				parsed++
			case strings.HasPrefix(name, "zfs_get_local_r_"):
				if _, err := parseZFSGetLocal(string(raw)); err != nil {
					t.Errorf("%s/%s: %v", set.Name(), name, err)
				}
				parsed++
			}
		}
	}
	// Four hosts have ZFS and between them seven pools, so a count this low
	// would mean the walk stopped finding captures rather than that they pass.
	if parsed < 10 {
		t.Errorf("parsed %d storage captures, want at least 10", parsed)
	}
}

// The whole chain on one host's real bytes: the adapter parses, the service
// drops the native properties, and the jail source matches each dataset to the
// jail mounted on it. The three paths are the ones isengard's jls capture
// reports, and the otter:* tags were set on that host with zfs set so that the
// headline feature of #2 -- find your jails by their tags -- has evidence
// rather than a constructed example.
func TestIsengardJailsCarryTheirRealStorage(t *testing.T) {
	runner := &replayRunner{t: t, set: "isengard", captures: withStorageRoots("tank")}
	source := storage.NewJailSource(storage.NewService(NewAdapter(runner), "tank"))

	got, err := source.StorageForPaths(context.Background(), []string{
		"/usr/local/jails/containers/postgres",
		"/usr/local/jails/containers/goodiesdb",
		"/usr/local/jails/containers/ottermq",
	})
	if err != nil {
		t.Fatalf("StorageForPaths() error = %v", err)
	}

	// Two commands for three jails, which is the batching #2 asks for.
	if len(runner.ran) != 2 {
		t.Errorf("ran %d commands for three jails, want 2: %v", len(runner.ran), runner.ran)
	}

	want := map[string]map[string]string{
		"/usr/local/jails/containers/ottermq":   {"otter:instance": "mq-01", "otter:tier": "broker"},
		"/usr/local/jails/containers/postgres":  {"otter:instance": "db-01"},
		"/usr/local/jails/containers/goodiesdb": nil, // deliberately untagged on the host
	}
	for path, wantProps := range want {
		entry, ok := got[path]
		if !ok {
			t.Errorf("%s was matched to no dataset", path)
			continue
		}
		if len(entry.Datasets) != 1 {
			t.Errorf("%s has %d datasets, want 1: %+v", path, len(entry.Datasets), entry.Datasets)
		}
		if !reflect.DeepEqual(entry.Properties, wantProps) {
			t.Errorf("%s properties = %v, want %v", path, entry.Properties, wantProps)
		}
	}

	// tank/jails carries a locally-set mountpoint, compression and atime. None
	// is a user property, and none of them reaches a jail.
	for path, entry := range got {
		for name := range entry.Properties {
			if !strings.Contains(name, ":") {
				t.Errorf("%s was given the native property %q", path, name)
			}
		}
	}

	// A jail cloned from a template refers to more than it uses, because the
	// template's blocks are still shared. Checked as a relation rather than as
	// byte counts, so it survives a re-capture (JD-007).
	postgres := got["/usr/local/jails/containers/postgres"].Datasets[0]
	if postgres.Referenced <= postgres.Used {
		t.Errorf("postgres refers to %d and uses %d; want a clone to refer to more than it uses",
			postgres.Referenced, postgres.Used)
	}
}
