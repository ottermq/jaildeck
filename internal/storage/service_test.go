package storage

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/audit"
)

// stubSystem answers from memory. The datasets and properties it carries are
// copied from testdata/hostfacts/isengard -- the names, the mountpoints and the
// byte figures are that host's -- but these tests are about the service's rules,
// not about parsing, so they do not read the captures.
type stubSystem struct {
	pools      []string
	poolsErr   error
	datasets   []Dataset
	datasetErr error
	properties []Property
	propErr    error
	roots      [][]string
	// The write half: every command the stub was asked to run, and an error to
	// make the next one fail.
	ran      []string
	writeErr error
	// The snapshot half.
	snapshots     []Snapshot
	snapshotErr   error
	holds         []Hold
	clones        map[string][]string
	dependencyErr error
	nextTXG       uint64
	// The move half.
	notMounted map[string]bool
	mountErr   error
	// mountErrFor fails only the named datasets, so a test can break one step
	// of a sequence without breaking every step that uses the same call.
	mountErrFor map[string]bool
	origins     map[string]string
	cloneErr    error
	promoteErr  error
}

func (s *stubSystem) Pools(context.Context) ([]string, error) { return s.pools, s.poolsErr }

// DatasetsUnder scopes to the roots, as zfs list -r does. It used not to, and
// a test of RetainedDatasets caught the lie: code that depends on the scoping
// passed against a stub that returned everything.
func (s *stubSystem) DatasetsUnder(_ context.Context, roots []string) ([]Dataset, error) {
	s.roots = append(s.roots, roots)
	if s.datasetErr != nil {
		return nil, s.datasetErr
	}
	found := make([]Dataset, 0, len(s.datasets))
	for _, dataset := range s.datasets {
		for _, root := range roots {
			if dataset.Name == root || strings.HasPrefix(dataset.Name, root+"/") {
				found = append(found, dataset)
				break
			}
		}
	}
	if len(found) == 0 {
		// zfs list exits non-zero for a path that does not exist, and that is
		// what "nothing is retained here" looks like from the caller's side.
		return nil, errors.New("cannot open: dataset does not exist")
	}
	return found, nil
}

func (s *stubSystem) LocalProperties(_ context.Context, roots []string) ([]Property, error) {
	s.roots = append(s.roots, roots)
	return s.properties, s.propErr
}

// The write half. Each records the command it would have run, in the same shape
// the freebsd adapter builds, so a test can assert on the argument array
// without a FreeBSD host.
func (s *stubSystem) CreateDataset(_ context.Context, name string, properties map[string]string) (Result, error) {
	s.ran = append(s.ran, stubCommand("create", name, properties))
	if s.writeErr != nil {
		return Result{Dataset: name, Command: stubCommand("create", name, properties)}, s.writeErr
	}
	s.datasets = append(s.datasets, Dataset{Name: name, Mountpoint: properties["mountpoint"]})
	for property, value := range properties {
		s.properties = append(s.properties, Property{Dataset: name, Name: property, Value: value})
	}
	return Result{Dataset: name, Command: stubCommand("create", name, properties)}, nil
}

func (s *stubSystem) SetProperties(_ context.Context, name string, properties map[string]string) (Result, error) {
	s.ran = append(s.ran, stubCommand("set", name, properties))
	if s.writeErr != nil {
		return Result{Dataset: name, Command: stubCommand("set", name, properties)}, s.writeErr
	}
	for property, value := range properties {
		replaced := false
		for i, p := range s.properties {
			if p.Dataset == name && p.Name == property {
				s.properties[i].Value, replaced = value, true
			}
		}
		if !replaced {
			s.properties = append(s.properties, Property{Dataset: name, Name: property, Value: value})
		}
	}
	return Result{Dataset: name, Command: stubCommand("set", name, properties)}, nil
}

func (s *stubSystem) InheritProperty(_ context.Context, name, property string) (Result, error) {
	command := "zfs inherit " + property + " " + name
	s.ran = append(s.ran, command)
	if s.writeErr != nil {
		return Result{Dataset: name, Command: command}, s.writeErr
	}
	s.properties = slices.DeleteFunc(s.properties, func(p Property) bool {
		return p.Dataset == name && p.Name == property
	})
	return Result{Dataset: name, Command: command}, nil
}

func stubCommand(verb, name string, properties map[string]string) string {
	names := make([]string, 0, len(properties))
	for property := range properties {
		names = append(names, property)
	}
	slices.Sort(names)
	command := "zfs " + verb
	for _, property := range names {
		if verb == "create" {
			command += " -o"
		}
		command += " " + property + "=" + properties[property]
	}
	return command + " " + name
}

func isengardTank() *stubSystem {
	return &stubSystem{
		pools: []string{"tank", "zroot", "backup"},
		datasets: []Dataset{
			{Name: "tank", Mountpoint: "none", Used: 2360819712, Available: 963201556480, Referenced: 98304},
			{Name: "tank/jails", Mountpoint: "/usr/local/jails", Used: 2331168768, Available: 963201556480, Referenced: 164872192},
			{Name: "tank/jails/containers/ottermq", Mountpoint: "/usr/local/jails/containers/ottermq", Used: 31850496, Available: 963201556480, Referenced: 416907264},
		},
		properties: []Property{
			{Dataset: "tank/jails", Name: "mountpoint", Value: "/usr/local/jails"},
			{Dataset: "tank/jails", Name: "compression", Value: "on"},
			{Dataset: "tank/jails/containers/ottermq", Name: "otter:instance", Value: "mq-01"},
			{Dataset: "tank/jails/containers/ottermq", Name: "com.sun:auto-snapshot", Value: "false"},
		},
	}
}

// A colon is the whole rule, and it is the rule that makes the map useful:
// without it every dataset reports the handful of native properties someone set
// by hand, none of which say whose jail it is.
func TestInventoryKeepsOnlyUserProperties(t *testing.T) {
	system := isengardTank()
	inventory, err := NewService(system, &fakeLogger{}, "tank").Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}

	want := map[string]map[string]string{
		"tank/jails/containers/ottermq": {"otter:instance": "mq-01", "com.sun:auto-snapshot": "false"},
	}
	if !reflect.DeepEqual(inventory.Properties, want) {
		t.Errorf("Properties = %+v, want %+v", inventory.Properties, want)
	}
	// Dropped from the properties, still present as a dataset: the native
	// mountpoint property is noise here, but the column it mirrors is not.
	if len(inventory.Datasets) != 3 {
		t.Errorf("Datasets = %d, want 3", len(inventory.Datasets))
	}
}

// The operator's pool narrows the read; no pool widens it to every imported
// pool rather than guessing one. Judging whether that pool is usable belongs to
// host readiness (JD-021), and this must not form a second opinion.
func TestInventoryScopesToTheConfiguredPool(t *testing.T) {
	tests := []struct {
		name      string
		pool      string
		wantRoots []string
	}{
		{name: "a named pool is the only root", pool: "tank", wantRoots: []string{"tank"}},
		{name: "no pool reads every imported one", pool: "", wantRoots: []string{"tank", "zroot", "backup"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := isengardTank()
			if _, err := NewService(system, &fakeLogger{}, tt.pool).Inventory(context.Background()); err != nil {
				t.Fatalf("Inventory() error = %v", err)
			}
			// Two reads, the datasets and the properties, and both must be
			// scoped the same way or the halves describe different hosts.
			if len(system.roots) != 2 {
				t.Fatalf("made %d reads, want 2: %v", len(system.roots), system.roots)
			}
			for _, roots := range system.roots {
				if !slices.Equal(roots, tt.wantRoots) {
					t.Errorf("read roots %v, want %v", roots, tt.wantRoots)
				}
			}
		})
	}
}

// ZFS is loaded and no pool is imported. There is nothing to read and nothing
// wrong: a host with no pool has no jails on ZFS either.
func TestInventoryWithNoPoolsIsEmptyNotAnError(t *testing.T) {
	system := isengardTank()
	system.pools = nil
	inventory, err := NewService(system, &fakeLogger{}, "").Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory.Datasets) != 0 || len(inventory.Properties) != 0 {
		t.Errorf("Inventory() = %+v, want nothing", inventory)
	}
	if len(system.roots) != 0 {
		t.Errorf("read %v with no pool to read", system.roots)
	}
	// Never null in JSON: a client iterating the datasets must not have to
	// check for it first.
	if inventory.Datasets == nil || inventory.Properties == nil {
		t.Error("Inventory() returned a nil collection, which marshals to null")
	}
}

func TestInventoryReportsEachFailure(t *testing.T) {
	boom := errors.New("zfs: pool is suspended")
	tests := []struct {
		name   string
		break_ func(*stubSystem)
	}{
		{name: "zpool failed", break_: func(s *stubSystem) { s.pools, s.poolsErr = nil, boom }},
		{name: "zfs list failed", break_: func(s *stubSystem) { s.datasetErr = boom }},
		{name: "zfs get failed", break_: func(s *stubSystem) { s.propErr = boom }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := isengardTank()
			tt.break_(system)
			pool := ""
			if tt.name != "zpool failed" {
				pool = "tank"
			}
			if _, err := NewService(system, &fakeLogger{}, pool).Inventory(context.Background()); !errors.Is(err, boom) {
				t.Errorf("Inventory() error = %v, want %v", err, boom)
			}
		})
	}
}

// fakeLogger collects audit entries. Local rather than from internal/audit's
// own test helpers: those are in that package's _test files and not importable.
type fakeLogger struct{ entries []audit.Entry }

func (f *fakeLogger) Log(_ context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return nil
}

// The snapshot half of the stub. Snapshots are ordered by a monotonic
// transaction group, as ZFS orders them, so "newer" means the same thing here
// as on a host.
func (s *stubSystem) Snapshots(_ context.Context, roots []string) ([]Snapshot, error) {
	if s.snapshotErr != nil {
		return nil, s.snapshotErr
	}
	found := make([]Snapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		for _, root := range roots {
			if snapshot.Dataset == root || strings.HasPrefix(snapshot.Dataset, root+"/") {
				found = append(found, snapshot)
				break
			}
		}
	}
	return found, nil
}

func (s *stubSystem) CreateSnapshot(_ context.Context, dataset, short string, recursive bool) (Result, error) {
	command := "zfs snapshot"
	if recursive {
		command += " -r"
	}
	command += " " + dataset + "@" + short
	s.ran = append(s.ran, command)
	if s.writeErr != nil {
		return Result{Dataset: dataset + "@" + short, Command: command}, s.writeErr
	}
	for _, target := range s.descendants(dataset, recursive) {
		s.nextTXG++
		s.snapshots = append(s.snapshots, Snapshot{
			Name: target + "@" + short, Dataset: target, Short: short, CreateTXG: s.nextTXG,
		})
	}
	return Result{Dataset: dataset + "@" + short, Command: command}, nil
}

func (s *stubSystem) DestroySnapshot(_ context.Context, dataset, short string, recursive bool) (Result, error) {
	command := "zfs destroy"
	if recursive {
		command += " -r"
	}
	command += " " + dataset + "@" + short
	s.ran = append(s.ran, command)
	if s.writeErr != nil {
		return Result{Dataset: dataset + "@" + short, Command: command}, s.writeErr
	}
	targets := s.descendants(dataset, recursive)
	s.snapshots = slices.DeleteFunc(s.snapshots, func(x Snapshot) bool {
		return x.Short == short && slices.Contains(targets, x.Dataset)
	})
	return Result{Dataset: dataset + "@" + short, Command: command}, nil
}

func (s *stubSystem) Rollback(_ context.Context, dataset, short string, destroyNewer bool) (Result, error) {
	command := "zfs rollback"
	if destroyNewer {
		command += " -r"
	}
	command += " " + dataset + "@" + short
	s.ran = append(s.ran, command)
	if s.writeErr != nil {
		return Result{Dataset: dataset + "@" + short, Command: command}, s.writeErr
	}
	var txg uint64
	for _, snapshot := range s.snapshots {
		if snapshot.Name == dataset+"@"+short {
			txg = snapshot.CreateTXG
		}
	}
	if destroyNewer {
		s.snapshots = slices.DeleteFunc(s.snapshots, func(x Snapshot) bool {
			return x.Dataset == dataset && x.CreateTXG > txg
		})
	}
	return Result{Dataset: dataset + "@" + short, Command: command}, nil
}

func (s *stubSystem) Clones(_ context.Context, snapshot string) ([]string, error) {
	return s.clones[snapshot], s.dependencyErr
}

func (s *stubSystem) Holds(_ context.Context, snapshot string) ([]Hold, error) {
	found := make([]Hold, 0)
	for _, hold := range s.holds {
		if hold.Snapshot == snapshot {
			found = append(found, hold)
		}
	}
	return found, s.dependencyErr
}

func (s *stubSystem) descendants(dataset string, recursive bool) []string {
	if !recursive {
		return []string{dataset}
	}
	targets := make([]string, 0, 2)
	for _, d := range s.datasets {
		if d.Name == dataset || strings.HasPrefix(d.Name, dataset+"/") {
			targets = append(targets, d.Name)
		}
	}
	return targets
}

// The move half of the stub. Rename carries descendants, properties and
// snapshots with the dataset, as zfs does, and leaves the mount alone, which
// is what -u means.
func (s *stubSystem) RenameDataset(_ context.Context, from, to string) (Result, error) {
	command := "zfs rename -u " + from + " " + to
	s.ran = append(s.ran, command)
	if s.writeErr != nil {
		return Result{Dataset: to, Command: command}, s.writeErr
	}
	parent := to[:strings.LastIndex(to, "/")]
	if !slices.ContainsFunc(s.datasets, func(d Dataset) bool { return d.Name == parent }) {
		return Result{Dataset: to, Command: command}, errors.New("cannot create '" + to + "': parent does not exist")
	}
	for i, d := range s.datasets {
		if d.Name == from || strings.HasPrefix(d.Name, from+"/") {
			s.datasets[i].Name = to + strings.TrimPrefix(d.Name, from)
			s.datasets[i].Mountpoint = "/usr/local/jails/retained/" + to[strings.LastIndex(to, "/")+1:]
		}
	}
	return Result{Dataset: to, Command: command}, nil
}

func (s *stubSystem) Mounted(_ context.Context, dataset string) (bool, error) {
	if s.notMounted[dataset] {
		return false, nil
	}
	return true, s.dependencyErr
}

func (s *stubSystem) Unmount(_ context.Context, dataset string) (Result, error) {
	s.ran = append(s.ran, "zfs unmount "+dataset)
	return Result{Dataset: dataset, Command: "zfs unmount " + dataset}, s.mountErrorFor(dataset)
}

func (s *stubSystem) Mount(_ context.Context, dataset string) (Result, error) {
	s.ran = append(s.ran, "zfs mount "+dataset)
	return Result{Dataset: dataset, Command: "zfs mount " + dataset}, s.mountErrorFor(dataset)
}

func (s *stubSystem) mountErrorFor(dataset string) error {
	if s.mountErrFor != nil && !s.mountErrFor[dataset] {
		return nil
	}
	return s.mountErr
}

func (s *stubSystem) CloneSnapshot(_ context.Context, snapshot, target string) (Result, error) {
	command := "zfs clone " + snapshot + " " + target
	s.ran = append(s.ran, command)
	if s.cloneErr != nil {
		return Result{Dataset: target, Command: command}, s.cloneErr
	}
	s.datasets = append(s.datasets, Dataset{Name: target, Mountpoint: s.mountpointFor(target)})
	if s.origins == nil {
		s.origins = map[string]string{}
	}
	s.origins[target] = snapshot
	return Result{Dataset: target, Command: command}, nil
}

func (s *stubSystem) Origin(_ context.Context, dataset string) (string, error) {
	return s.origins[dataset], s.dependencyErr
}

// mountpointFor derives a mountpoint the way an inherited one works: the
// parent's, with the dataset's own name appended.
func (s *stubSystem) mountpointFor(name string) string {
	parent := name[:strings.LastIndex(name, "/")]
	for _, d := range s.datasets {
		if d.Name == parent {
			return d.Mountpoint + name[strings.LastIndex(name, "/"):]
		}
	}
	return "/" + name
}

// PromoteDataset swaps the clone relationship, as zfs does: the promoted
// dataset takes its former origin's origin, and the former origin becomes a
// clone of it. Modelled rather than stubbed out, because a test of the
// jail-to-template sequence that did not move the dependency would prove
// nothing about the one thing that sequence exists to get right.
func (s *stubSystem) PromoteDataset(_ context.Context, dataset string) (Result, error) {
	command := "zfs promote " + dataset
	s.ran = append(s.ran, command)
	if s.promoteErr != nil {
		return Result{Dataset: dataset, Command: command}, s.promoteErr
	}
	origin, cloned := s.origins[dataset]
	if !cloned {
		return Result{Dataset: dataset, Command: command},
			errors.New("cannot promote '" + dataset + "': not a cloned filesystem")
	}
	former := origin[:strings.Index(origin, "@")]
	short := origin[strings.Index(origin, "@")+1:]

	if grandparent, ok := s.origins[former]; ok {
		s.origins[dataset] = grandparent
	} else {
		delete(s.origins, dataset)
	}
	s.origins[former] = dataset + "@" + short

	for i, snapshot := range s.snapshots {
		if snapshot.Dataset == former && snapshot.Short == short {
			s.snapshots[i].Dataset = dataset
			s.snapshots[i].Name = dataset + "@" + short
		}
	}
	return Result{Dataset: dataset, Command: command}, nil
}
