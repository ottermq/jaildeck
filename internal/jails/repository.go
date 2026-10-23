package jails

import (
	"context"
)

type OperationResult struct {
	Jail    Jail
	Command string
}

type JailSystem interface {
	List(ctx context.Context) ([]Jail, error)
	Start(ctx context.Context, name string) (OperationResult, error)
	Stop(ctx context.Context, name string) (OperationResult, error)
	Restart(ctx context.Context, name string) (OperationResult, error)
}

// Dataset is a ZFS dataset belonging to a jail, as the listing reports it.
//
// This is deliberately the jails domain's own type rather than the storage
// domain's: the port below is defined by the package that needs it and speaks
// that package's types (JD-010), which is what keeps internal/jails free of
// any import of internal/storage -- and testable with no ZFS in sight.
type Dataset struct {
	Name       string `json:"name"`
	Mountpoint string `json:"mountpoint"`
	// Bytes, not the "1.41G" zfs list prints by default, so a client can
	// compare and total them.
	Used       int64 `json:"used"`
	Referenced int64 `json:"referenced"`
	Available  int64 `json:"available"`
}

// JailStorage is the storage half of one jail's listing.
type JailStorage struct {
	// Datasets are the jail's own dataset and everything mounted beneath it,
	// in the order zfs listed them.
	Datasets []Dataset
	// Properties are the ZFS user properties of the dataset mounted at the
	// jail's path -- the jail's own, not a child's. An automation client tags
	// its jails with these (otter:instance) and finds them again from the
	// listing alone, with nothing stored in Jail Deck (JD-004, JD-011).
	Properties map[string]string
}

// DatasetSource answers which datasets belong to a jail. It takes the paths
// rather than the names because a jail's path is what ties it to ZFS: a
// dataset is the jail's when it is mounted at that path or under it, which
// stays true for a jail placed outside the conventional <pool>/jails/
// containers/<name> layout, and reports nothing for a jail not on ZFS at all.
//
// One call answers every jail, because the implementation reads the host once
// (#2: "one batched zfs list/zfs get call, not one per jail").
type DatasetSource interface {
	// StorageForPaths answers for each path given. A path with no dataset of
	// its own is absent from the map rather than present and empty, so the
	// caller can tell "no dataset is mounted there" from "that dataset has no
	// children". An empty path is never answered: a jail with no path matches
	// nothing rather than everything.
	StorageForPaths(ctx context.Context, paths []string) (map[string]JailStorage, error)
}
