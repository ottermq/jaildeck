package fake

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/storage"
)

// The seed is arranged so every refusal #4 names is reachable without a
// FreeBSD host: nginx has two plain snapshots, one held, and redis has one
// with a clone. A dashboard that can only ever show the happy path is not much
// of a dashboard.
func (s *StorageSystem) seedSnapshots() {
	s.snapshots = []storage.Snapshot{
		s.snapshot("zroot/jails/nginx", "before-upgrade"),
		s.snapshot("zroot/jails/nginx/logs", "before-upgrade"),
		s.snapshot("zroot/jails/nginx", "nightly"),
		s.snapshot("zroot/jails/redis", "cloned-from"),
	}
	s.holds = []storage.Hold{{Snapshot: "zroot/jails/nginx@nightly", Tag: "keep-for-audit"}}
	s.clones = map[string][]string{"zroot/jails/redis@cloned-from": {"zroot/jails/redis-copy"}}
}

func (s *StorageSystem) snapshot(dataset, short string) storage.Snapshot {
	s.nextTXG++
	return storage.Snapshot{
		Name: dataset + "@" + short, Dataset: dataset, Short: short,
		Used: 0, Referenced: 96 << 10,
		Created:   time.Unix(1792700000+int64(s.nextTXG), 0).UTC(),
		CreateTXG: s.nextTXG,
	}
}

func (s *StorageSystem) Snapshots(_ context.Context, roots []string) ([]storage.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := make([]storage.Snapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		if under(snapshot.Dataset, roots) {
			found = append(found, snapshot)
		}
	}
	return found, nil
}

func (s *StorageSystem) CreateSnapshot(_ context.Context, dataset, short string, recursive bool) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	command := "zfs snapshot"
	if recursive {
		command += " -r"
	}
	command += " " + dataset + "@" + short
	result := storage.Result{Dataset: dataset + "@" + short, Command: command}

	for _, target := range s.targetsLocked(dataset, recursive) {
		name := target + "@" + short
		if slices.ContainsFunc(s.snapshots, func(x storage.Snapshot) bool { return x.Name == name }) {
			return result, fmt.Errorf("cannot create snapshot '%s': dataset already exists", name)
		}
		s.snapshots = append(s.snapshots, s.snapshot(target, short))
	}
	return result, nil
}

func (s *StorageSystem) DestroySnapshot(_ context.Context, dataset, short string, recursive bool) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	command := "zfs destroy"
	if recursive {
		command += " -r"
	}
	command += " " + dataset + "@" + short

	targets := s.targetsLocked(dataset, recursive)
	s.snapshots = slices.DeleteFunc(s.snapshots, func(x storage.Snapshot) bool {
		return x.Short == short && slices.Contains(targets, x.Dataset)
	})
	return storage.Result{Dataset: dataset + "@" + short, Command: command}, nil
}

func (s *StorageSystem) Rollback(_ context.Context, dataset, short string, destroyNewer bool) (storage.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	command := "zfs rollback"
	if destroyNewer {
		command += " -r"
	}
	command += " " + dataset + "@" + short
	result := storage.Result{Dataset: dataset + "@" + short, Command: command}

	var txg uint64
	for _, snapshot := range s.snapshots {
		if snapshot.Name == dataset+"@"+short {
			txg = snapshot.CreateTXG
		}
	}
	if txg == 0 {
		return result, fmt.Errorf("cannot rollback to '%s@%s': no such snapshot", dataset, short)
	}
	if destroyNewer {
		s.snapshots = slices.DeleteFunc(s.snapshots, func(x storage.Snapshot) bool {
			return x.Dataset == dataset && x.CreateTXG > txg
		})
	}
	return result, nil
}

func (s *StorageSystem) Clones(_ context.Context, snapshot string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clones[snapshot], nil
}

func (s *StorageSystem) Holds(_ context.Context, snapshot string) ([]storage.Hold, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := make([]storage.Hold, 0)
	for _, hold := range s.holds {
		if hold.Snapshot == snapshot {
			found = append(found, hold)
		}
	}
	return found, nil
}

// targetsLocked is the dataset itself, plus its descendants when recursive --
// which is what zfs snapshot -r and zfs destroy -r cover.
func (s *StorageSystem) targetsLocked(dataset string, recursive bool) []string {
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
