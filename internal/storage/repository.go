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

	// Snapshots lists the snapshots of each root and its descendants.
	// Snapshots are a separate type that zfs list omits unless asked for.
	Snapshots(ctx context.Context, roots []string) ([]Snapshot, error)
	// CreateSnapshot runs zfs snapshot; recursive adds -r, which takes the
	// dataset and every descendant at one point in time.
	CreateSnapshot(ctx context.Context, dataset, snapshot string, recursive bool) (Result, error)
	// DestroySnapshot runs zfs destroy; recursive adds -r, which is the same
	// snapshot name on every descendant and not the -R that would take clones.
	DestroySnapshot(ctx context.Context, dataset, snapshot string, recursive bool) (Result, error)
	// Rollback runs zfs rollback. destroyNewer adds -r, which here means
	// "destroy the snapshots taken after this one" -- not "recurse into
	// children", which zfs rollback cannot do at all.
	Rollback(ctx context.Context, dataset, snapshot string, destroyNewer bool) (Result, error)

	// Clones names the datasets cloned from a snapshot, empty when there are
	// none. Asked before a destroy rather than read out of its error, so the
	// refusal happens before anything destructive runs.
	Clones(ctx context.Context, snapshot string) ([]string, error)
	// Holds names the holds on a snapshot, for the same reason: zfs destroy
	// reports that one exists without saying whose it is.
	Holds(ctx context.Context, snapshot string) ([]Hold, error)

	// RenameDataset runs zfs rename -u. The -u is the whole point: a plain
	// rename unmounts the dataset and remounts it at its new place as part of
	// the same command, which on a running jail makes /data disappear from
	// under whatever is using it, mid-operation. With -u the mount is left
	// exactly where it was and moving it becomes a separate, timed act.
	RenameDataset(ctx context.Context, from, to string) (Result, error)
	// Mounted reports whether the dataset is mounted right now. Asked as its
	// own question because both of the calls below fail when it is wrong:
	// unmounting what is not mounted and mounting what already is are each an
	// error, not a no-op.
	Mounted(ctx context.Context, dataset string) (bool, error)
	Unmount(ctx context.Context, dataset string) (Result, error)
	Mount(ctx context.Context, dataset string) (Result, error)

	// CloneSnapshot runs zfs clone. A jail's root is a clone of a template's
	// snapshot (JD-007) -- on the deployment target all three jails are clones
	// of templates/15.1-RELEASE@base -- so re-cloning one onto a newer
	// snapshot is how a jail is upgraded.
	CloneSnapshot(ctx context.Context, snapshot, target string) (Result, error)
	// Origin is the snapshot a dataset was cloned from, empty when it was not
	// cloned from anything.
	Origin(ctx context.Context, dataset string) (string, error)
	// PromoteDataset runs zfs promote, which reverses a clone relationship:
	// the clone takes ownership of the snapshot it was made from, and its
	// former origin becomes a clone of it.
	//
	// Without it, a template cloned from a jail's snapshot keeps the jail
	// alive forever -- zfs destroy on that jail answers "filesystem has
	// dependent clones" and names the template. Measured on the deployment
	// target, both before and after (JD-027).
	PromoteDataset(ctx context.Context, dataset string) (Result, error)
	// DestroyDataset runs zfs destroy. recursive adds -r, which is needed for
	// anything that has snapshots -- zfs refuses a bare destroy then and says
	// so. Never -R: that takes dependent clones with it, which is the thing
	// every destroy here refuses to do.
	DestroyDataset(ctx context.Context, dataset string, recursive bool) (Result, error)
}

// Result is what a write actually ran, so the service can audit the command
// rather than a reconstruction of it, as host.DatasetResult does.
type Result struct {
	Dataset string
	Command string
}
