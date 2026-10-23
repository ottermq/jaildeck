package storage

import "time"

// Dataset is one line of the batched zfs list: a filesystem's identity, where
// it is mounted, and its three space figures in bytes.
//
// The sizes are bytes because the API is read by automation first (JD-016) and
// a client has to be able to compare and total them. zfs list prints "1.41G"
// by default and exact bytes only under -p, so the adapter passes -p and
// nothing here ever holds a formatted size.
type Dataset struct {
	Name string
	// Mountpoint is the property's value, which is not always a path: a pool
	// root is often "none", and so is every dataset on a replication target.
	Mountpoint string
	// Used is the space the dataset and its descendants occupy; Referenced is
	// what this dataset alone refers to, which for a clone includes the blocks
	// it still shares with its origin -- the reason JD-007 puts quotas on a
	// child dataset rather than on a jail's root.
	Used       int64
	Referenced int64
	// Available is free space in the dataset's pool, so every dataset of one
	// pool reports the same figure unless a quota narrows it.
	Available int64
}

// Property is one locally-set ZFS property of one dataset. Only the name,
// because "local" is the only source the adapter asks for.
type Property struct {
	Dataset string
	Name    string
	Value   string
}

// Inventory is the host's storage as two batched reads: every dataset under
// the roots, and the user properties set on them, grouped by dataset name.
//
// Properties holds only ZFS *user* properties -- a name containing a colon,
// such as otter:instance. That filter is what makes the map worth returning:
// every dataset also reports a handful of locally-set native properties
// (mountpoint, compression, atime) that say nothing about whose jail it is.
type Inventory struct {
	Datasets []Dataset
	// Properties are the ZFS user properties -- the tags a client sets and
	// reads back (JD-004), keyed by dataset then by name.
	Properties map[string]map[string]string
	// Native are the allowlisted native properties a dataset sets *locally*,
	// keyed the same way. One that a dataset inherits is absent, because it is
	// the parent's setting and not this dataset's: that is the same line the
	// write side draws, where `zfs inherit` removes a local value rather than
	// setting an empty one (JD-023).
	Native map[string]map[string]string
}

// Snapshot is one line of the batched snapshot listing.
type Snapshot struct {
	// Name is the full "dataset@snapshot" as zfs prints it, which is also what
	// confirm= has to equal on a destroy or a rollback (JD-024).
	Name    string
	Dataset string
	// Short is the part after the @, which is what a client names in a URL.
	Short string
	Used  int64
	// Referenced is what the snapshot still refers to; Used is what it alone
	// keeps alive, which is usually 0 until something changes after it.
	Referenced int64
	Created    time.Time
	// CreateTXG orders snapshots exactly. Created is a second-granularity
	// timestamp and two snapshots taken in the same second share it, so "which
	// of these is newer" cannot be answered from it -- and that question is
	// precisely what decides whether a rollback destroys anything. The
	// transaction group is ZFS's own total order and never ties.
	CreateTXG uint64
}

// Hold is one zfs hold on a snapshot: a named reason it cannot be destroyed,
// which zfs destroy reports without naming (it says to run zfs holds instead).
type Hold struct {
	Snapshot string
	Tag      string
}
