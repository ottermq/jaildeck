package fake

import (
	"context"
	"strings"

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
	datasets   []storage.Dataset
	properties []storage.Property
}

func NewStorageSystem() *StorageSystem {
	const gib = 1 << 30
	return &StorageSystem{
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
}

func (s *StorageSystem) Pools(ctx context.Context) ([]string, error) {
	return []string{"zroot"}, nil
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
