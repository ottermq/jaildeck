package storage

import "context"

// StorageSystem observes ZFS. Like host.HostSystem, every method returns what
// the host said and nothing more: which datasets belong to a jail, and which
// of their properties are worth showing, are the service's judgement, so the
// rules test on any OS with no ZFS anywhere near them.
//
// Each method is one command for the whole host, never one per jail (#2). The
// roots bound the recursion: a single pool when the operator named one, every
// imported pool otherwise. Naming one root rather than several keeps the only
// failure the root itself being gone -- zfs list given a path that does not
// exist exits 1 and still prints the paths that do, so a vanished dataset in a
// longer list would turn a complete answer into a failed command.
type StorageSystem interface {
	// Pools lists the imported pools in the order zpool prints them. A host
	// without ZFS does not lack the binary: zpool exits non-zero.
	Pools(ctx context.Context) ([]string, error)
	// DatasetsUnder lists each root and everything beneath it. Snapshots are
	// absent: they are a separate type that zfs list omits unless asked for.
	//
	// Named Under rather than Datasets because host.HostSystem already has a
	// Datasets of its own, with a different signature, and one *freebsd.Adapter
	// implements both ports. That the two exist at all is worth removing later:
	// readiness asks zfs for name,mountpoint to check the layout while this
	// asks for the same datasets with their sizes, which is two commands where
	// one would do.
	DatasetsUnder(ctx context.Context, roots []string) ([]Dataset, error)
	// LocalProperties returns every property set locally on those datasets,
	// native ones included. The colon filter belongs to the service.
	LocalProperties(ctx context.Context, roots []string) ([]Property, error)

	// CreateDataset runs zfs create with properties as -o options.
	CreateDataset(ctx context.Context, name string, properties map[string]string) (Result, error)
	// SetProperties runs one zfs set for all of them, native or user: which
	// names are allowed is the service's rule, not the adapter's.
	SetProperties(ctx context.Context, name string, properties map[string]string) (Result, error)
	// InheritProperty runs zfs inherit, which unsets one property so the
	// dataset takes its parent's value again -- the only way to remove a user
	// property, and one property per call, because that is zfs's own shape.
	InheritProperty(ctx context.Context, name, property string) (Result, error)
}

// Result is what a write actually ran, so the service can audit the command
// rather than a reconstruction of it, as host.DatasetResult does.
type Result struct {
	Dataset string
	Command string
}
