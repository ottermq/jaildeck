package storage

import (
	"context"
	"fmt"
	"net/http"
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
	parent, err := s.ensureRetained(ctx, source)
	if err != nil {
		return MoveResult{}, err
	}
	return s.moveDataset(ctx, source, parent+"/"+name)
}

// RestoreDataset moves one back in, under a jail's dataset.
func (s *Service) RestoreDataset(ctx context.Context, pool, name, target string) (MoveResult, error) {
	if err := checkChildName(name); err != nil {
		return MoveResult{}, err
	}
	return s.moveDataset(ctx, retainedParent(pool)+"/"+name, target)
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

// moveDataset renames and then carries the mount across, in that order.
//
// The three commands are separate on purpose. zfs rename without -u does all
// of this itself, and does the unmount in the middle of the rename, where a
// running jail loses its filesystem without anything having decided that was
// acceptable. Done this way the rename is invisible and the unmount is a
// deliberate step with its own audit line.
func (s *Service) moveDataset(ctx context.Context, from, to string) (MoveResult, error) {
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
	if !wasMounted {
		// Nothing was mounted, so there is nothing to carry. Mounting it now
		// would be a change nobody asked for.
		moved.Mountpoint = mountpointOf(ctx, s, to)
		return moved, nil
	}

	// Until this runs, the dataset is named in one place and mounted in
	// another -- and a jail with a foreign filesystem mounted inside it cannot
	// be destroyed: zfs answers "pool or dataset is busy".
	unmount, unmountErr := s.system.Unmount(ctx, to)
	s.logWrite(ctx, OperationMountDataset, unmount, unmountErr)
	if unmountErr != nil {
		return MoveResult{}, toWriteError("unmount", to, unmountErr)
	}
	mount, mountErr := s.system.Mount(ctx, to)
	s.logWrite(ctx, OperationMountDataset, mount, mountErr)
	if mountErr != nil {
		return MoveResult{}, toWriteError("mount", to, mountErr)
	}
	moved.Remounted = true
	moved.Mountpoint = mountpointOf(ctx, s, to)
	return moved, nil
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
