package storage

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
	Datasets   []Dataset
	Properties map[string]map[string]string
}
