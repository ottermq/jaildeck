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

// DatasetChange is what ensuring a dataset did. It decides the status code:
// created is 201, a conflict is 409, anything else 200.
type DatasetChange struct {
	Dataset string            `json:"dataset"`
	Created bool              `json:"created"`
	Applied map[string]string `json:"applied"`
	// Conflict is set when the dataset exists somewhere other than where the
	// request said. Mountpoint is settable only at creation (JD-023), so this
	// is not a change the API will make -- and reporting both values says more
	// than refusing would.
	Conflict *PropertyConflict `json:"conflict,omitempty"`
}

type PropertyConflict struct {
	Property string `json:"property"`
	Want     string `json:"want"`
	Have     string `json:"have"`
}

// DatasetWriter is the write half, kept apart from DatasetSource because a
// listing must not need write access to a port in order to read one.
//
// Both take the jail's path for the same reason StorageForPaths does: the path
// is what ties a jail to ZFS. child names a dataset directly beneath the
// jail's own; an empty child means the jail's own dataset, which is where a
// client's otter:instance tag belongs.
type DatasetWriter interface {
	// EnsureDataset creates the child with properties, or brings an existing
	// one to them. Idempotent: a client that timed out repeats the call and
	// the result says where it stands (JD-016).
	EnsureDataset(ctx context.Context, jailPath, child string, properties map[string]string) (DatasetChange, error)
	// SetUserProperties merges properties in; a nil value removes one. It
	// returns what the dataset carries afterwards, read back rather than
	// assumed.
	SetUserProperties(ctx context.Context, jailPath, child string, properties map[string]*string) (map[string]string, error)
}
