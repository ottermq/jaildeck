package storage

import (
	"context"
	"strings"
)

// Service reads the host's storage. It holds no state: everything it returns
// is derived from zfs on the call, because ZFS is already the record (JD-011).
type Service struct {
	system StorageSystem
	// pool is JAILDECK_ZFS_POOL. Empty is not an error here and no pool is
	// adopted by guesswork: the read simply widens to every imported pool.
	// Judging whether the operator's pool is usable is host readiness' job
	// (JD-021), and duplicating that judgement is how the two would disagree.
	pool string
}

func NewService(system StorageSystem, pool string) *Service {
	return &Service{system: system, pool: pool}
}

// Inventory reads the datasets and their user properties in two commands, both
// scoped to the same roots so the two halves describe the same thing.
func (s *Service) Inventory(ctx context.Context) (Inventory, error) {
	roots, err := s.roots(ctx)
	if err != nil {
		return Inventory{}, err
	}
	inventory := Inventory{Datasets: make([]Dataset, 0), Properties: map[string]map[string]string{}}
	if len(roots) == 0 {
		// ZFS is there and no pool is imported. Nothing to read, which is not
		// a failure: a host with no pool has no jails on ZFS either.
		return inventory, nil
	}

	datasets, err := s.system.DatasetsUnder(ctx, roots)
	if err != nil {
		return Inventory{}, err
	}
	inventory.Datasets = datasets

	properties, err := s.system.LocalProperties(ctx, roots)
	if err != nil {
		return Inventory{}, err
	}
	for _, p := range properties {
		if !isUserProperty(p.Name) {
			continue
		}
		if inventory.Properties[p.Dataset] == nil {
			inventory.Properties[p.Dataset] = map[string]string{}
		}
		inventory.Properties[p.Dataset][p.Name] = p.Value
	}
	return inventory, nil
}

// isUserProperty reports whether name is a ZFS user property rather than one
// of the native ones. A colon is the whole rule: zfs(8) reserves names without
// one for ZFS itself and requires user properties to carry a module prefix,
// which is why otter:instance and com.sun:auto-snapshot both qualify.
func isUserProperty(name string) bool {
	return strings.Contains(name, ":")
}

func (s *Service) roots(ctx context.Context) ([]string, error) {
	if s.pool != "" {
		return []string{s.pool}, nil
	}
	return s.system.Pools(ctx)
}
