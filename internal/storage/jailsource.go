package storage

import (
	"context"
	"strings"

	"github.com/ottermq/jaildeck/internal/jails"
)

// JailSource implements jails.DatasetSource on top of the storage service.
//
// It lives here, not in internal/jails, for the reason JD-010 gives for every
// adapter: the side that implements a port depends on the side that defines
// it, never the reverse. internal/jails names what it needs and imports
// nothing; this package knows both its own types and that port's.
type JailSource struct {
	service *Service
}

func NewJailSource(service *Service) *JailSource {
	return &JailSource{service: service}
}

// StorageForPaths reads the host once and assigns each dataset to the jail
// path it is mounted at or under.
func (s *JailSource) StorageForPaths(ctx context.Context, paths []string) (map[string]jails.JailStorage, error) {
	inventory, err := s.service.Inventory(ctx)
	if err != nil {
		return nil, err
	}

	found := map[string]jails.JailStorage{}
	for _, dataset := range inventory.Datasets {
		path, isRoot, ok := ownerOf(dataset.Mountpoint, paths)
		if !ok {
			continue
		}
		entry := found[path]
		if entry.Datasets == nil {
			entry.Datasets = make([]jails.Dataset, 0, 1)
		}
		entry.Datasets = append(entry.Datasets, jails.Dataset{
			Name:       dataset.Name,
			Mountpoint: dataset.Mountpoint,
			Used:       dataset.Used,
			Referenced: dataset.Referenced,
			Available:  dataset.Available,
		})
		// The jail's properties are its own dataset's, not a child's: a
		// consumer tags the jail, and a child inheriting the tag would make
		// one jail look like several.
		if isRoot {
			entry.Properties = inventory.Properties[dataset.Name]
		}
		found[path] = entry
	}
	return found, nil
}

// ownerOf picks which jail path a mountpoint belongs to, and whether it is that
// jail's own dataset or one beneath it.
//
// The longest matching path wins. That only matters for a jail inside another
// jail's directory, where /jails/a/inner/data is under both /jails/a and
// /jails/a/inner -- and it belongs to the inner one.
func ownerOf(mountpoint string, paths []string) (owner string, isRoot bool, ok bool) {
	// zfs reports an unmounted dataset's mountpoint as "none" or "-", and every
	// dataset of a replication target reads that way (JD-021). This is a
	// short-circuit, not the rule that excludes them: neither word is an
	// absolute path, so the comparisons below would reject them anyway --
	// deleting this line changes no test. It earns its place by naming the two
	// shapes and by skipping the loop for all 23 datasets of a backup pool.
	if mountpoint == "" || mountpoint == "none" || mountpoint == "-" {
		return "", false, false
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		switch {
		case mountpoint == path:
			// An exact match is as specific as it gets, so nothing longer can
			// win and the search can stop.
			return path, true, true
		case strings.HasPrefix(mountpoint, strings.TrimSuffix(path, "/")+"/"):
			// The trailing separator is what keeps /jails/ab out of /jails/a.
			if len(path) > len(owner) {
				owner, isRoot, ok = path, false, true
			}
		}
	}
	return owner, isRoot, ok
}

var _ jails.DatasetSource = (*JailSource)(nil)

// EnsureDataset creates <the jail's dataset>/<child>, or brings an existing one
// to the properties given.
func (s *JailSource) EnsureDataset(ctx context.Context, jailPath, child string, properties map[string]string) (jails.DatasetChange, error) {
	target, err := s.datasetFor(ctx, jailPath, child)
	if err != nil {
		return jails.DatasetChange{}, err
	}
	change, err := s.service.EnsureDataset(ctx, target, properties)
	if err != nil {
		return jails.DatasetChange{}, err
	}
	out := jails.DatasetChange{Dataset: change.Dataset, Created: change.Created, Applied: change.Applied}
	if change.Conflict != nil {
		out.Conflict = &jails.PropertyConflict{
			Property: change.Conflict.Property,
			Want:     change.Conflict.Want,
			Have:     change.Conflict.Have,
		}
	}
	return out, nil
}

// SetUserProperties tags the jail's own dataset, or one beneath it.
func (s *JailSource) SetUserProperties(ctx context.Context, jailPath, child string, properties map[string]*string) (map[string]string, error) {
	target, err := s.datasetFor(ctx, jailPath, child)
	if err != nil {
		return nil, err
	}
	return s.service.SetUserProperties(ctx, target, properties)
}

// datasetFor resolves a jail path, and optionally a child name, to a full ZFS
// dataset name. An empty child is the jail's own dataset.
//
// The jail's own dataset must already exist -- it is the thing the jail runs
// from. The child need not: creating it is the point.
func (s *JailSource) datasetFor(ctx context.Context, jailPath, child string) (string, error) {
	if jailPath == "" {
		return "", jails.ErrJailNotOnZFS
	}
	inventory, err := s.service.Inventory(ctx)
	if err != nil {
		return "", err
	}
	root := ""
	for _, dataset := range inventory.Datasets {
		if dataset.Mountpoint == jailPath {
			root = dataset.Name
			break
		}
	}
	if root == "" {
		return "", jails.ErrJailNotOnZFS
	}
	if child == "" {
		return root, nil
	}
	if err := checkChildName(child); err != nil {
		return "", err
	}
	return root + "/" + child, nil
}

var _ jails.DatasetWriter = (*JailSource)(nil)
