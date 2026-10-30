package storage

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// DatasetDestroy is what destroying one dataset took, or what stopped it
// (#120). Snapshots are named either way: they are what goes with it, and an
// operator about to confirm should see them first.
type DatasetDestroy struct {
	Dataset   string   `json:"dataset"`
	Snapshots []string `json:"snapshots"`
	Destroyed bool     `json:"destroyed"`
	// Blocked is set when something kept on purpose is in the way. Nothing
	// ran: the dataset is still there, as it was.
	Blocked *DatasetBlock `json:"blocked,omitempty"`
}

// DatasetBlock names what is in the way. Children are datasets of the
// dataset's own: zfs destroy -r takes them without a word (measured, #120),
// and a destroy that names one dataset destroys that one.
type DatasetBlock struct {
	Reason   string   `json:"reason"`
	Children []string `json:"children"`
	Clones   []string `json:"clones"`
	Holds    []string `json:"holds"`
}

// DestroyRetained destroys <pool>/jails/retained/<name> and its snapshots:
// the end of a retain, which until now was a root shell (JD-025). confirm is
// the short name, as a jail's destroy is confirmed with the jail's.
func (s *Service) DestroyRetained(ctx context.Context, name, confirm string) (DatasetDestroy, error) {
	if err := checkChildName(name); err != nil {
		return DatasetDestroy{}, err
	}
	dataset, err := s.retainedNamed(ctx, name)
	if err != nil {
		return DatasetDestroy{}, err
	}
	if confirm != name {
		return DatasetDestroy{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"this destroys %s and its snapshots; it needs confirm=%s", dataset, name))
	}
	return s.destroyOne(ctx, dataset)
}

// retainedNamed finds a retained dataset on whichever pool has it, the way a
// template is found. Only <pool>/jails/retained is looked in: the name is one
// path segment, so it cannot reach anywhere else.
func (s *Service) retainedNamed(ctx context.Context, name string) (string, error) {
	roots, err := s.roots(ctx)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		candidate := retainedParent(root) + "/" + name
		if dataset, err := s.datasetNamed(ctx, candidate); err == nil && dataset != nil {
			return candidate, nil
		}
	}
	return "", common.NewAppError(http.StatusNotFound, "nothing retained under the name "+name)
}

// destroyOne destroys dataset and its snapshots, after asking what would stop
// it. The asking is not decoration: zfs refuses a clone or a hold itself, but
// it destroys a dataset's own children under -r silently, and a child of the
// dataset is not what the caller named.
//
// -r only when there are snapshots, and never -R, the same as a jail's root:
// -R would take the dependent clones this refuses.
func (s *Service) destroyOne(ctx context.Context, dataset string) (DatasetDestroy, error) {
	found, err := s.system.DatasetsUnder(ctx, []string{dataset})
	if err != nil || !containsDataset(found, dataset) {
		// zfs list exits 1 for a dataset that is not there. A client
		// repeating a destroy after a timeout learns from 404 that it is gone.
		return DatasetDestroy{}, common.NewAppError(http.StatusNotFound, "no dataset named "+dataset)
	}
	result := DatasetDestroy{Dataset: dataset, Snapshots: make([]string, 0)}

	children := make([]string, 0)
	for _, d := range found {
		if strings.HasPrefix(d.Name, dataset+"/") {
			children = append(children, d.Name)
		}
	}
	// Its own snapshots, and its children's when it has children -- which
	// refuses below, naming everything -r would have taken.
	snapshots, err := s.SnapshotsOf(ctx, dataset)
	if err != nil {
		return DatasetDestroy{}, err
	}
	own := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		own = append(own, snapshot.Name)
	}
	result.Snapshots = own

	block, err := s.blockersFor(ctx, own)
	if err != nil {
		return DatasetDestroy{}, err
	}
	if block != nil || len(children) > 0 {
		result.Blocked = datasetBlock(dataset, children, block)
		return result, nil
	}

	// Unmounted first, on its own. zfs destroy -r takes the snapshots before
	// it unmounts, so one refused as busy -- a host process with a file open
	// on it -- has already lost them (measured, #120). A busy unmount refuses
	// with nothing destroyed.
	mounted, err := s.system.Mounted(ctx, dataset)
	if err != nil {
		return result, toWriteError("read the mount state of", dataset, err)
	}
	if mounted {
		unmount, unmountErr := s.system.Unmount(ctx, dataset)
		s.logWrite(ctx, OperationMountDataset, unmount, unmountErr)
		if unmountErr != nil {
			return result, toWriteError("unmount", dataset, unmountErr)
		}
	}

	destroyed, destroyErr := s.system.DestroyDataset(ctx, dataset, len(own) > 0)
	s.logWrite(ctx, OperationDestroyDataset, destroyed, destroyErr)
	if destroyErr != nil {
		// Mounted again, so a destroy that did not happen leaves the dataset
		// as it was found. Best effort: the destroy's error is the answer.
		if mounted {
			mount, mountErr := s.system.Mount(ctx, dataset)
			s.logWrite(ctx, OperationMountDataset, mount, mountErr)
		}
		return result, toWriteError("destroy", dataset, destroyErr)
	}
	result.Destroyed = true
	return result, nil
}

func datasetBlock(dataset string, children []string, snapshots *SnapshotBlock) *DatasetBlock {
	block := &DatasetBlock{Children: children, Clones: make([]string, 0), Holds: make([]string, 0)}
	reasons := make([]string, 0, 3)
	if len(children) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d dataset(s) of its own", len(children)))
	}
	if snapshots != nil {
		block.Clones, block.Holds = snapshots.Clones, snapshots.Holds
		if len(block.Clones) > 0 {
			reasons = append(reasons, fmt.Sprintf("%d dependent clone(s)", len(block.Clones)))
		}
		if len(block.Holds) > 0 {
			reasons = append(reasons, fmt.Sprintf("%d hold(s)", len(block.Holds)))
		}
	}
	block.Reason = "refused: " + dataset + " has " + strings.Join(reasons, " and ") +
		". None is overridden here: a dataset of its own is destroyed by naming it, and " +
		"releasing a hold or destroying a clone is a separate, deliberate act."
	return block
}

func containsDataset(datasets []Dataset, name string) bool {
	for _, d := range datasets {
		if d.Name == name {
			return true
		}
	}
	return false
}
