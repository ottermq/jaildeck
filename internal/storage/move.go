package storage

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

const (
	OperationRenameDataset = "rename-dataset"
	OperationMountDataset  = "mount-dataset"
)

// RetainedChild is where a dataset lives once it has outlived its jail. It sits
// beside media, templates and containers under <pool>/jails, so one destroy of
// that tree is still the whole story -- but it is deliberately NOT part of the
// layout host readiness checks (JD-021): a host that has never retained
// anything is not missing something.
const RetainedChild = "retained"

// MountpointProperty is where a retained dataset was mounted, relative to its
// jail's path, when that mountpoint was set on the dataset itself (#121). A
// locally set mountpoint travels with a rename, so retained as it is the
// dataset would stay mounted inside the jail it left -- and that jail could not
// be destroyed. It inherits instead while retained, and the restore rebuilds
// the path under the jail it goes back into: var/db/postgres under
// /usr/local/jails/containers/db2 is /usr/local/jails/containers/db2/var/db/postgres.
const MountpointProperty = "jaildeck:mountpoint"

// MoveResult is what a move did. Remounted says whether the filesystem was
// actually carried to its new mountpoint, which is false when it was not
// mounted to begin with.
type MoveResult struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Mountpoint string `json:"mountpoint"`
	Remounted  bool   `json:"remounted"`
}

// RetainDataset moves a dataset out of its jail to <pool>/jails/retained/<name>,
// creating that parent the first time one is needed.
//
// This is what makes "destroy the jail, keep the data" possible: zfs destroy -r
// on a jail takes its children and their snapshots with it (JD-007), so
// anything worth keeping has to stop being a child first.
func (s *Service) RetainDataset(ctx context.Context, source, name string) (MoveResult, error) {
	if err := checkChildName(name); err != nil {
		return MoveResult{}, err
	}
	// Asked before anything is created: a mountpoint outside the jail is
	// refused, and refused before the retained parent exists.
	relative, err := s.localMountpoint(ctx, source)
	if err != nil {
		return MoveResult{}, err
	}
	parent, err := s.ensureRetained(ctx, source)
	if err != nil {
		return MoveResult{}, err
	}
	var mountpoint mountpointChange
	if relative != "" {
		mountpoint = mountpointChange{record: relative}
	}
	return s.moveDataset(ctx, source, parent+"/"+name, mountpoint)
}

// RestoreDataset moves one back in, under a jail's dataset.
func (s *Service) RestoreDataset(ctx context.Context, pool, name, target string) (MoveResult, error) {
	if err := checkChildName(name); err != nil {
		return MoveResult{}, err
	}
	from := retainedParent(pool) + "/" + name
	var mountpoint mountpointChange
	relative, err := s.recordedMountpoint(ctx, from)
	if err != nil {
		return MoveResult{}, err
	}
	if relative != "" {
		jail, err := s.datasetNamed(ctx, path.Dir(target))
		if err != nil {
			return MoveResult{}, err
		}
		if jail == nil || !strings.HasPrefix(jail.Mountpoint, "/") {
			return MoveResult{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s was mounted at %s inside its jail, and %s has no path to put that under",
				from, relative, path.Dir(target)))
		}
		// The record is a property anyone can set: one that climbs out of
		// the jail is refused, as a mountpoint outside it is at create.
		restored := path.Join(jail.Mountpoint, relative)
		if !strings.HasPrefix(restored, jail.Mountpoint+"/") {
			return MoveResult{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s records %s=%s, which is not a path inside a jail", from, MountpointProperty, relative))
		}
		mountpoint = mountpointChange{restore: restored}
	}
	return s.moveDataset(ctx, from, target, mountpoint)
}

// mountpointChange is what a move does to a mountpoint set on the dataset
// itself (#121): on retain, record it relative to the jail and inherit; on
// restore, set it under the new jail and forget the record. Neither, for a
// dataset that inherits its mountpoint, which is most of them.
type mountpointChange struct {
	record  string
	restore string
}

// localMountpoint is a dataset's mountpoint relative to its jail's path, when
// it is set on the dataset itself; empty when it is inherited. A mountpoint
// outside the jail is refused: retaining a dataset that never lived in the
// jail is not what retain is for, and Jail Deck refuses to create one.
func (s *Service) localMountpoint(ctx context.Context, dataset string) (string, error) {
	value, err := s.localProperty(ctx, dataset, "mountpoint")
	if err != nil || value == "" {
		return "", err
	}
	jail, err := s.datasetNamed(ctx, path.Dir(dataset))
	if err != nil {
		return "", err
	}
	if jail != nil && strings.HasPrefix(jail.Mountpoint, "/") {
		if relative, ok := strings.CutPrefix(value, jail.Mountpoint+"/"); ok && relative != "" {
			return relative, nil
		}
	}
	where := path.Dir(dataset)
	if jail != nil {
		where += " (" + jail.Mountpoint + ")"
	}
	return "", common.NewAppError(http.StatusConflict, fmt.Sprintf(
		"%s has its mountpoint set to %s, which is not inside its jail %s; "+
			"set it inside the jail, or inherit it, before retaining it", dataset, value, where))
}

// recordedMountpoint is the MountpointProperty a retain left, empty for none.
func (s *Service) recordedMountpoint(ctx context.Context, dataset string) (string, error) {
	return s.localProperty(ctx, dataset, MountpointProperty)
}

func (s *Service) localProperty(ctx context.Context, dataset, name string) (string, error) {
	properties, err := s.system.LocalProperties(ctx, []string{dataset})
	if err != nil {
		return "", toWriteError("read the properties of", dataset, err)
	}
	for _, p := range properties {
		if p.Dataset == dataset && p.Name == name {
			return p.Value, nil
		}
	}
	return "", nil
}

// RetainedDatasets lists what is being kept, which is what a client needs in
// order to know what it can restore.
func (s *Service) RetainedDatasets(ctx context.Context) ([]Dataset, error) {
	roots, err := s.roots(ctx)
	if err != nil {
		return nil, err
	}
	retained := make([]Dataset, 0)
	for _, root := range roots {
		parent := retainedParent(root)
		// A pool with nothing retained has no such dataset, and zfs list exits
		// non-zero for it. That is an answer, not a failure.
		datasets, err := s.system.DatasetsUnder(ctx, []string{parent})
		if err != nil {
			continue
		}
		for _, dataset := range datasets {
			if dataset.Name != parent {
				retained = append(retained, dataset)
			}
		}
	}
	return retained, nil
}

// RetainedSnapshots names the snapshots of every retained dataset, by
// dataset: what a destroy of one would take with it (#120), listed before it
// is confirmed. One zfs list per pool, not one per dataset.
func (s *Service) RetainedSnapshots(ctx context.Context) (map[string][]string, error) {
	roots, err := s.roots(ctx)
	if err != nil {
		return nil, err
	}
	byDataset := make(map[string][]string)
	for _, root := range roots {
		// Nothing retained on this pool, the same answer RetainedDatasets
		// takes from the same failure.
		snapshots, err := s.SnapshotsOf(ctx, retainedParent(root))
		if err != nil {
			continue
		}
		for _, snapshot := range snapshots {
			byDataset[snapshot.Dataset] = append(byDataset[snapshot.Dataset], snapshot.Name)
		}
	}
	return byDataset, nil
}

// moveDataset renames and then carries the mount across, in that order.
//
// The three commands are separate on purpose. zfs rename without -u does all
// of this itself, and does the unmount in the middle of the rename, where a
// running jail loses its filesystem without anything having decided that was
// acceptable. Done this way the rename is invisible and the unmount is a
// deliberate step with its own audit line.
func (s *Service) moveDataset(ctx context.Context, from, to string, mountpoint mountpointChange) (MoveResult, error) {
	source, err := s.datasetNamed(ctx, from)
	if err != nil {
		return MoveResult{}, err
	}
	if source == nil {
		return MoveResult{}, common.NewAppError(http.StatusNotFound, "no dataset named "+from)
	}
	existing, err := s.datasetNamed(ctx, to)
	if err != nil {
		return MoveResult{}, err
	}
	if existing != nil {
		return MoveResult{}, common.NewAppError(http.StatusConflict, to+" already exists")
	}

	// Asked before the rename, because afterwards the dataset answers for its
	// new name and the old mount is the thing that has to be undone.
	wasMounted, err := s.system.Mounted(ctx, from)
	if err != nil {
		return MoveResult{}, toWriteError("read the mount state of", from, err)
	}

	result, renameErr := s.system.RenameDataset(ctx, from, to)
	s.logWrite(ctx, OperationRenameDataset, result, renameErr)
	if renameErr != nil {
		return MoveResult{}, toWriteError("rename", from, renameErr)
	}

	moved := MoveResult{From: from, To: to}
	if !wasMounted && mountpoint == (mountpointChange{}) {
		// Nothing was mounted, so there is nothing to carry. Mounting it now
		// would be a change nobody asked for.
		moved.Mountpoint = mountpointOf(ctx, s, to)
		return moved, nil
	}

	// Until this runs, the dataset is named in one place and mounted in
	// another -- and a jail with a foreign filesystem mounted inside it cannot
	// be destroyed: zfs answers "pool or dataset is busy".
	if wasMounted {
		unmount, unmountErr := s.system.Unmount(ctx, to)
		s.logWrite(ctx, OperationMountDataset, unmount, unmountErr)
		if unmountErr != nil {
			return MoveResult{}, toWriteError("unmount", to, unmountErr)
		}
	}
	if err := s.changeMountpoint(ctx, to, mountpoint); err != nil {
		return MoveResult{}, err
	}
	// Setting or inheriting a mountpoint mounts the dataset by itself, even
	// one that was not mounted -- measured, #121 -- so the mount is asked about
	// rather than assumed, and a dataset that was not mounted is left as it
	// was found.
	mounted := false
	if mountpoint != (mountpointChange{}) {
		var err error
		if mounted, err = s.system.Mounted(ctx, to); err != nil {
			return MoveResult{}, toWriteError("read the mount state of", to, err)
		}
	}
	switch {
	case wasMounted && !mounted:
		mount, mountErr := s.system.Mount(ctx, to)
		s.logWrite(ctx, OperationMountDataset, mount, mountErr)
		if mountErr != nil {
			return MoveResult{}, toWriteError("mount", to, mountErr)
		}
	case !wasMounted && mounted:
		unmount, unmountErr := s.system.Unmount(ctx, to)
		s.logWrite(ctx, OperationMountDataset, unmount, unmountErr)
		if unmountErr != nil {
			return MoveResult{}, toWriteError("unmount", to, unmountErr)
		}
	}
	moved.Remounted = wasMounted
	moved.Mountpoint = mountpointOf(ctx, s, to)
	return moved, nil
}

// changeMountpoint records and inherits a retained dataset's own mountpoint,
// or puts a restored one's back under its new jail and forgets the record,
// each step audited on its own.
func (s *Service) changeMountpoint(ctx context.Context, dataset string, change mountpointChange) error {
	switch {
	case change.record != "":
		result, err := s.system.SetProperties(ctx, dataset, map[string]string{MountpointProperty: change.record})
		s.logWrite(ctx, OperationSetProperties, result, err)
		if err != nil {
			return toWriteError("record the mountpoint of", dataset, err)
		}
		result, err = s.system.InheritProperty(ctx, dataset, "mountpoint")
		s.logWrite(ctx, OperationInheritProperty, result, err)
		if err != nil {
			return toWriteError("inherit the mountpoint of", dataset, err)
		}
	case change.restore != "":
		result, err := s.system.SetProperties(ctx, dataset, map[string]string{"mountpoint": change.restore})
		s.logWrite(ctx, OperationSetProperties, result, err)
		if err != nil {
			return toWriteError("set the mountpoint of", dataset, err)
		}
		result, err = s.system.InheritProperty(ctx, dataset, MountpointProperty)
		s.logWrite(ctx, OperationInheritProperty, result, err)
		if err != nil {
			return toWriteError("forget the recorded mountpoint of", dataset, err)
		}
	}
	return nil
}

// ensureRetained creates <pool>/jails/retained the first time it is needed,
// deriving the pool from the dataset being moved rather than from
// configuration: the dataset is already on exactly one pool, and that is the
// one its retained sibling belongs on.
func (s *Service) ensureRetained(ctx context.Context, source string) (string, error) {
	pool, _, ok := strings.Cut(source, "/")
	if !ok {
		return "", common.NewAppError(http.StatusBadRequest, fmt.Sprintf("%q is not a dataset in a pool", source))
	}
	parent := retainedParent(pool)
	existing, err := s.datasetNamed(ctx, parent)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return parent, nil
	}
	result, createErr := s.system.CreateDataset(ctx, parent, nil)
	s.logWrite(ctx, OperationCreateDataset, result, createErr)
	if createErr != nil {
		return "", toWriteError("create", parent, createErr)
	}
	return parent, nil
}

func retainedParent(pool string) string {
	return pool + "/jails/" + RetainedChild
}

// mountpointOf reports where the dataset says it belongs now. Best effort: the
// move has already happened and failing to read a property afterwards should
// not turn a completed move into an error.
func mountpointOf(ctx context.Context, s *Service, name string) string {
	dataset, err := s.datasetNamed(ctx, name)
	if err != nil || dataset == nil {
		return ""
	}
	return dataset.Mountpoint
}
