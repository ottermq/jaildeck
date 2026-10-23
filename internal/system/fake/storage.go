package fake

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ottermq/jaildeck/internal/storage"
)

// StorageSystem is an in-memory ZFS whose datasets line up with the fake
// jails' paths, so the web interface and the request collection have real
// shapes to render without a FreeBSD box.
//
// The seed is deliberately awkward in three ways, each matching something a
// real host does: "nginx" owns a child dataset, so a jail is more than one
// line; "redis" owns none, so it is exactly one; and a dataset mounted
// nowhere ("none") sits in the pool, as every dataset of a replication target
// does (JD-021) -- it must never be matched to a jail. The fake "postgres"
// jail has no path at all, and so has no datasets here either.
type StorageSystem struct {
	// Writes mutate this in place so the web interface and the collection show
	// what a create or a tag actually did, and the HTTP server serves requests
	// concurrently, so it is guarded.
	mu         sync.Mutex
	datasets   []storage.Dataset
	properties []storage.Property
	snapshots  []storage.Snapshot
	holds      []storage.Hold
	origins    map[string]string
	// nextTXG stands in for ZFS's transaction group counter, which is what
	// orders snapshots exactly. Monotonic, as the real one is.
	nextTXG uint64
}

func NewStorageSystem() *StorageSystem {
	const gib = 1 << 30
	system := &StorageSystem{
		datasets: []storage.Dataset{
			{Name: "zroot", Mountpoint: "none", Used: 12 * gib, Available: 400 * gib, Referenced: 96 << 10},
			{Name: "zroot/jails", Mountpoint: "/fake/jail", Used: 9 * gib, Available: 400 * gib, Referenced: 157 << 20},
			{Name: "zroot/jails/nginx", Mountpoint: "/fake/jail/nginx", Used: 2 * gib, Available: 400 * gib, Referenced: 1536 << 20},
			{Name: "zroot/jails/nginx/logs", Mountpoint: "/fake/jail/nginx/logs", Used: 412 << 20, Available: 400 * gib, Referenced: 412 << 20},
			{Name: "zroot/jails/redis", Mountpoint: "/fake/jail/redis", Used: 740 << 20, Available: 400 * gib, Referenced: 1208 << 20},
			{Name: "zroot/backup", Mountpoint: "none", Used: 31 * gib, Available: 400 * gib, Referenced: 96 << 10},
		},
		properties: []storage.Property{
			// A native property set locally, which the colon rule drops: every
			// real dataset has a few of these and none of them say whose jail
			// it is.
			{Dataset: "zroot/jails", Name: "compression", Value: "lz4"},
			{Dataset: "zroot/jails/nginx", Name: "otter:instance", Value: "web-01"},
			{Dataset: "zroot/jails/nginx", Name: "otter:tier", Value: "frontend"},
			// On the child, not the jail: a jail's listing must report its own
			// properties and not a child's.
			{Dataset: "zroot/jails/nginx/logs", Name: "otter:retention", Value: "14d"},
			{Dataset: "zroot/jails/redis", Name: "otter:instance", Value: "cache-01"},
		},
	}
	system.seedSnapshots()
	return system
}

func (s *StorageSystem) Pools(ctx context.Context) ([]string, error) {
	return []string{"zroot"}, nil
}

// CreateDataset gives the new dataset its parent's mountpoint with its own name
// appended unless one was asked for, as zfs does, and inherits nothing else:
// the seeded sizes are made up either way.
func (s *StorageSystem) CreateDataset(_ context.Context, name string, properties map[string]string) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := storage.Result{Dataset: name, Command: fakeCommand("create", properties, name)}
	if slices.ContainsFunc(s.datasets, func(d storage.Dataset) bool { return d.Name == name }) {
		return result, fmt.Errorf("cannot create %q: dataset already exists", name)
	}

	mountpoint := properties["mountpoint"]
	if mountpoint == "" {
		parent, child, _ := cutLast(name, "/")
		mountpoint = s.mountpointOf(parent) + "/" + child
	}
	const gib = 1 << 30
	s.datasets = append(s.datasets, storage.Dataset{
		Name: name, Mountpoint: mountpoint, Used: 96 << 10, Referenced: 96 << 10, Available: 400 * gib,
	})
	slices.SortFunc(s.datasets, func(a, b storage.Dataset) int { return strings.Compare(a.Name, b.Name) })

	for property, value := range properties {
		s.setLocked(name, property, value)
	}
	return result, nil
}

func (s *StorageSystem) SetProperties(_ context.Context, name string, properties map[string]string) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for property, value := range properties {
		s.setLocked(name, property, value)
	}
	return storage.Result{Dataset: name, Command: fakeCommand("set", properties, name)}, nil
}

// InheritProperty drops the local value. The real zfs would then show the
// parent's, which this does not model: nothing here inherits anything.
func (s *StorageSystem) InheritProperty(_ context.Context, name, property string) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.properties = slices.DeleteFunc(s.properties, func(p storage.Property) bool {
		return p.Dataset == name && p.Name == property
	})
	return storage.Result{Dataset: name, Command: "zfs inherit " + property + " " + name}, nil
}

func (s *StorageSystem) setLocked(dataset, property, value string) {
	for i, p := range s.properties {
		if p.Dataset == dataset && p.Name == property {
			s.properties[i].Value = value
			return
		}
	}
	s.properties = append(s.properties, storage.Property{Dataset: dataset, Name: property, Value: value})
	slices.SortFunc(s.properties, func(a, b storage.Property) int {
		if c := strings.Compare(a.Dataset, b.Dataset); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
}

func (s *StorageSystem) mountpointOf(name string) string {
	for _, d := range s.datasets {
		if d.Name == name {
			return d.Mountpoint
		}
	}
	return "none"
}

func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// fakeCommand renders the zfs command this would have been, in the same name
// order the real adapter uses, so the audit log reads the same in fake mode.
func fakeCommand(verb string, properties map[string]string, name string) string {
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

func (s *StorageSystem) DatasetsUnder(ctx context.Context, roots []string) ([]storage.Dataset, error) {
	datasets := make([]storage.Dataset, 0, len(s.datasets))
	for _, d := range s.datasets {
		if under(d.Name, roots) {
			datasets = append(datasets, d)
		}
	}
	return datasets, nil
}

func (s *StorageSystem) LocalProperties(ctx context.Context, roots []string) ([]storage.Property, error) {
	properties := make([]storage.Property, 0, len(s.properties))
	for _, p := range s.properties {
		if under(p.Dataset, roots) {
			properties = append(properties, p)
		}
	}
	return properties, nil
}

// under reports whether a dataset is one of the roots or beneath one, which is
// what -r means to zfs.
func under(name string, roots []string) bool {
	for _, root := range roots {
		if name == root || strings.HasPrefix(name, root+"/") {
			return true
		}
	}
	return false
}

var _ storage.StorageSystem = (*StorageSystem)(nil)

// RenameDataset moves the dataset and everything beneath it, and re-derives the
// mountpoints the way an inherited one follows its parent. The mount itself is
// left alone, which is what -u means and what the three calls below exist to
// deal with.
func (s *StorageSystem) RenameDataset(_ context.Context, from, to string) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := storage.Result{Dataset: to, Command: "zfs rename -u " + from + " " + to}
	if slices.ContainsFunc(s.datasets, func(d storage.Dataset) bool { return d.Name == to }) {
		return result, fmt.Errorf("cannot rename '%s': dataset already exists", from)
	}
	parent, _, _ := cutLast(to, "/")
	if !slices.ContainsFunc(s.datasets, func(d storage.Dataset) bool { return d.Name == parent }) {
		return result, fmt.Errorf("cannot create '%s': parent does not exist", to)
	}
	moved := false
	for i, d := range s.datasets {
		if d.Name != from && !strings.HasPrefix(d.Name, from+"/") {
			continue
		}
		suffix := strings.TrimPrefix(d.Name, from)
		s.datasets[i].Name = to + suffix
		s.datasets[i].Mountpoint = s.mountpointOf(parent) + "/" + lastComponent(to) + suffix
		moved = true
	}
	if !moved {
		return result, fmt.Errorf("cannot open '%s': dataset does not exist", from)
	}
	for i, p := range s.properties {
		if p.Dataset == from || strings.HasPrefix(p.Dataset, from+"/") {
			s.properties[i].Dataset = to + strings.TrimPrefix(p.Dataset, from)
		}
	}
	for i, snapshot := range s.snapshots {
		if snapshot.Dataset == from || strings.HasPrefix(snapshot.Dataset, from+"/") {
			s.snapshots[i].Dataset = to + strings.TrimPrefix(snapshot.Dataset, from)
			s.snapshots[i].Name = s.snapshots[i].Dataset + "@" + snapshot.Short
		}
	}
	slices.SortFunc(s.datasets, func(a, b storage.Dataset) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

// Mounted is true for everything with a real mountpoint, which is as much as
// this needs to model: nothing here is ever deliberately left unmounted.
func (s *StorageSystem) Mounted(_ context.Context, dataset string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.datasets {
		if d.Name == dataset {
			return d.Mountpoint != "none" && d.Mountpoint != "-" && d.Mountpoint != "", nil
		}
	}
	return false, fmt.Errorf("cannot open '%s': dataset does not exist", dataset)
}

func (s *StorageSystem) Unmount(_ context.Context, dataset string) (storage.Result, error) {
	return storage.Result{Dataset: dataset, Command: "zfs unmount " + dataset}, nil
}

func (s *StorageSystem) Mount(_ context.Context, dataset string) (storage.Result, error) {
	return storage.Result{Dataset: dataset, Command: "zfs mount " + dataset}, nil
}

func lastComponent(name string) string {
	_, last, found := cutLast(name, "/")
	if !found {
		return name
	}
	return last
}

func (s *StorageSystem) CloneSnapshot(_ context.Context, snapshot, target string) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := storage.Result{Dataset: target, Command: "zfs clone " + snapshot + " " + target}
	if slices.ContainsFunc(s.datasets, func(d storage.Dataset) bool { return d.Name == target }) {
		return result, fmt.Errorf("cannot create '%s': dataset already exists", target)
	}
	if !slices.ContainsFunc(s.snapshots, func(x storage.Snapshot) bool { return x.Name == snapshot }) {
		return result, fmt.Errorf("cannot open '%s': dataset does not exist", snapshot)
	}
	parent, child, _ := cutLast(target, "/")
	const gib = 1 << 30
	s.datasets = append(s.datasets, storage.Dataset{
		Name: target, Mountpoint: s.mountpointOf(parent) + "/" + child,
		Used: 96 << 10, Referenced: 372 << 20, Available: 400 * gib,
	})
	slices.SortFunc(s.datasets, func(a, b storage.Dataset) int { return strings.Compare(a.Name, b.Name) })
	s.origins[target] = snapshot
	return result, nil
}

func (s *StorageSystem) Origin(_ context.Context, dataset string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.origins[dataset], nil
}

// PromoteDataset swaps the clone relationship: the promoted dataset takes the
// origin it had, and whatever it was cloned from becomes a clone of it.
func (s *StorageSystem) PromoteDataset(_ context.Context, dataset string) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := storage.Result{Dataset: dataset, Command: "zfs promote " + dataset}
	origin, cloned := s.origins[dataset]
	if !cloned {
		return result, fmt.Errorf("cannot promote '%s': not a cloned filesystem", dataset)
	}
	former, snapshot, _ := cutLast(origin, "@")

	// The promoted dataset inherits whatever its former origin was cloned
	// from, and that former origin now hangs off the promoted one.
	s.origins[dataset] = s.origins[former]
	if s.origins[dataset] == "" {
		delete(s.origins, dataset)
	}
	s.origins[former] = dataset + "@" + snapshot

	// The snapshot moves with the ownership, which is what lets the former
	// origin be destroyed afterwards. Nothing else has to move: Clones() reads
	// the origins, and those have already been swapped above.
	for i, x := range s.snapshots {
		if x.Dataset == former && x.Short == snapshot {
			s.snapshots[i].Dataset = dataset
			s.snapshots[i].Name = dataset + "@" + snapshot
		}
	}
	return result, nil
}

func (s *StorageSystem) DestroyDataset(_ context.Context, dataset string, recursive bool) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	command := "zfs destroy"
	if recursive {
		command += " -r"
	}
	command += " " + dataset
	result := storage.Result{Dataset: dataset, Command: command}

	if !slices.ContainsFunc(s.datasets, func(d storage.Dataset) bool { return d.Name == dataset }) {
		return result, fmt.Errorf("cannot open '%s': dataset does not exist", dataset)
	}
	// zfs refuses a bare destroy of anything with snapshots or children, and
	// says to use -r. Modelled, because the code under test is what decides
	// to pass it.
	if !recursive {
		for _, x := range s.snapshots {
			if x.Dataset == dataset {
				return result, fmt.Errorf("cannot destroy '%s': filesystem has children", dataset)
			}
		}
	}
	s.datasets = slices.DeleteFunc(s.datasets, func(d storage.Dataset) bool {
		return d.Name == dataset || (recursive && strings.HasPrefix(d.Name, dataset+"/"))
	})
	s.snapshots = slices.DeleteFunc(s.snapshots, func(x storage.Snapshot) bool {
		return x.Dataset == dataset || (recursive && strings.HasPrefix(x.Dataset, dataset+"/"))
	})
	delete(s.origins, dataset)
	return result, nil
}
