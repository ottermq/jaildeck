package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ottermq/jaildeck/internal/jails"
)

// The ordinary case, with isengard's real names and byte figures: the jail's
// own dataset, a child beneath it, and the jail's own user property.
func TestStorageForPathsMatchesByMountpoint(t *testing.T) {
	system := &stubSystem{
		pools: []string{"tank"},
		datasets: []Dataset{
			{Name: "tank", Mountpoint: "none", Used: 2360819712, Available: 963201556480, Referenced: 98304},
			{Name: "tank/jails/containers", Mountpoint: "/usr/local/jails/containers", Used: 1550581760, Available: 963201556480, Referenced: 106496},
			{Name: "tank/jails/containers/ottermq", Mountpoint: "/usr/local/jails/containers/ottermq", Used: 31850496, Available: 963201556480, Referenced: 416907264},
			{Name: "tank/jails/containers/ottermq/data", Mountpoint: "/usr/local/jails/containers/ottermq/data", Used: 4096, Available: 963201556480, Referenced: 4096},
			{Name: "tank/jails/containers/postgres", Mountpoint: "/usr/local/jails/containers/postgres", Used: 1514057728, Available: 963201556480, Referenced: 1891282944},
		},
		properties: []Property{
			{Dataset: "tank/jails/containers/ottermq", Name: "otter:instance", Value: "mq-01"},
			// On the child. A jail reports its own properties, not a child's,
			// or one jail would look like several to a client searching by tag.
			{Dataset: "tank/jails/containers/ottermq/data", Name: "otter:instance", Value: "mq-01-data"},
		},
	}
	source := NewJailSource(NewService(system, &fakeLogger{}, "tank"))

	got, err := source.StorageForPaths(context.Background(), []string{
		"/usr/local/jails/containers/ottermq",
		"/usr/local/jails/containers/postgres",
		"/usr/local/jails/containers/redis", // configured, no dataset of its own
	})
	if err != nil {
		t.Fatalf("StorageForPaths() error = %v", err)
	}

	want := map[string]jails.JailStorage{
		"/usr/local/jails/containers/ottermq": {
			Datasets: []jails.Dataset{
				{Name: "tank/jails/containers/ottermq", Mountpoint: "/usr/local/jails/containers/ottermq", Used: 31850496, Referenced: 416907264, Available: 963201556480},
				{Name: "tank/jails/containers/ottermq/data", Mountpoint: "/usr/local/jails/containers/ottermq/data", Used: 4096, Referenced: 4096, Available: 963201556480},
			},
			Properties: map[string]string{"otter:instance": "mq-01"},
		},
		"/usr/local/jails/containers/postgres": {
			Datasets: []jails.Dataset{
				{Name: "tank/jails/containers/postgres", Mountpoint: "/usr/local/jails/containers/postgres", Used: 1514057728, Referenced: 1891282944, Available: 963201556480},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StorageForPaths() = %+v\nwant %+v", got, want)
	}
	// A jail with no dataset is absent rather than present and empty, so the
	// caller can tell "nothing is mounted there" from "that one has no
	// children".
	if _, ok := got["/usr/local/jails/containers/redis"]; ok {
		t.Error("a jail with no dataset of its own was answered")
	}
	// And the pool root, mounted nowhere, belongs to no jail.
	for path, entry := range got {
		for _, d := range entry.Datasets {
			if d.Name == "tank" {
				t.Errorf("the pool root was matched to %s", path)
			}
		}
	}
}

func TestOwnerOf(t *testing.T) {
	// Two jails whose paths share a prefix, and one inside the other's
	// directory. Both are the cases a plain strings.HasPrefix gets wrong.
	paths := []string{"/jails/a", "/jails/ab", "/jails/a/inner", ""}
	tests := []struct {
		name       string
		mountpoint string
		wantOwner  string
		wantRoot   bool
		wantOK     bool
	}{
		{name: "the jail's own dataset", mountpoint: "/jails/a", wantOwner: "/jails/a", wantRoot: true, wantOK: true},
		{name: "a child", mountpoint: "/jails/a/data", wantOwner: "/jails/a", wantOK: true},
		{name: "a sibling whose path starts the same way", mountpoint: "/jails/ab", wantOwner: "/jails/ab", wantRoot: true, wantOK: true},
		{name: "a child of that sibling", mountpoint: "/jails/abc/data"},
		{name: "the longest path wins", mountpoint: "/jails/a/inner/data", wantOwner: "/jails/a/inner", wantOK: true},
		{name: "the nested jail itself", mountpoint: "/jails/a/inner", wantOwner: "/jails/a/inner", wantRoot: true, wantOK: true},
		{name: "above every jail", mountpoint: "/jails"},
		{name: "unmounted", mountpoint: "none"},
		{name: "no mountpoint at all", mountpoint: "-"},
		{name: "empty", mountpoint: ""},
		{name: "elsewhere", mountpoint: "/srv/media"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, isRoot, ok := ownerOf(tt.mountpoint, paths)
			if owner != tt.wantOwner || isRoot != tt.wantRoot || ok != tt.wantOK {
				t.Errorf("ownerOf(%q) = %q, %v, %v; want %q, %v, %v",
					tt.mountpoint, owner, isRoot, ok, tt.wantOwner, tt.wantRoot, tt.wantOK)
			}
		})
	}
}

// An empty path must match nothing. It is the fake adapter's stopped jail and a
// real stanza that sets no path, and a prefix test against "" would hand that
// jail every dataset on the host.
func TestStorageForPathsIgnoresAnEmptyPath(t *testing.T) {
	system := &stubSystem{
		pools:    []string{"tank"},
		datasets: []Dataset{{Name: "tank/jails", Mountpoint: "/usr/local/jails"}},
	}
	got, err := NewJailSource(NewService(system, &fakeLogger{}, "tank")).StorageForPaths(context.Background(), []string{""})
	if err != nil {
		t.Fatalf("StorageForPaths() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("StorageForPaths([\"\"]) = %+v, want nothing", got)
	}
}

func TestStorageForPathsPropagatesAFailedRead(t *testing.T) {
	boom := errors.New("zfs: pool is suspended")
	system := &stubSystem{pools: []string{"tank"}, datasetErr: boom}
	got, err := NewJailSource(NewService(system, &fakeLogger{}, "tank")).StorageForPaths(context.Background(), []string{"/usr/local/jails"})
	if !errors.Is(err, boom) {
		t.Errorf("StorageForPaths() = %+v, %v; want %v", got, err, boom)
	}
}
