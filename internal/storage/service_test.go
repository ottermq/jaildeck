package storage

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
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
}

func (s *stubSystem) Pools(context.Context) ([]string, error) { return s.pools, s.poolsErr }

func (s *stubSystem) DatasetsUnder(_ context.Context, roots []string) ([]Dataset, error) {
	s.roots = append(s.roots, roots)
	return s.datasets, s.datasetErr
}

func (s *stubSystem) LocalProperties(_ context.Context, roots []string) ([]Property, error) {
	s.roots = append(s.roots, roots)
	return s.properties, s.propErr
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
	inventory, err := NewService(system, "tank").Inventory(context.Background())
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
			if _, err := NewService(system, tt.pool).Inventory(context.Background()); err != nil {
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
	inventory, err := NewService(system, "").Inventory(context.Background())
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
			if _, err := NewService(system, pool).Inventory(context.Background()); !errors.Is(err, boom) {
				t.Errorf("Inventory() error = %v, want %v", err, boom)
			}
		})
	}
}
