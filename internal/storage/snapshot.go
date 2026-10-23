package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

const (
	OperationCreateSnapshot  = "create-snapshot"
	OperationDestroySnapshot = "destroy-snapshot"
	OperationRollback        = "rollback"
)

var ErrSnapshotNotFound = common.NewAppError(http.StatusNotFound, "snapshot not found")

// SnapshotResult is what a snapshot call did. Exactly one of Created and
// Destroyed is ever non-empty, and both are present-but-empty rather than
// absent so a client never has to check for null.
type SnapshotResult struct {
	Snapshot  string   `json:"snapshot"`
	Created   []string `json:"created"`
	Destroyed []string `json:"destroyed"`
	// Existed reports a create that found the snapshot already there and ran
	// nothing. The retry an automation client makes after a timeout (JD-016).
	Existed bool `json:"existed,omitempty"`
	// Blocked is set when the operation refused. Nothing ran.
	Blocked *SnapshotBlock `json:"blocked,omitempty"`
}

// SnapshotBlock says why, and names what is in the way -- which is the whole
// point of checking before running rather than reporting zfs's complaint after.
type SnapshotBlock struct {
	Reason string `json:"reason"`
	// Newer are the snapshots a rollback would destroy. Passing
	// destroy_newer=true accepts losing exactly these.
	Newer []string `json:"newer,omitempty"`
	// Clones and Holds cannot be overridden through this API at all: taking a
	// clone with its origin needs zfs destroy -R, which this never passes, and
	// a hold exists precisely to say "not this one".
	Clones []string `json:"clones,omitempty"`
	Holds  []string `json:"holds,omitempty"`
}

// SnapshotsOf lists a dataset's snapshots and its descendants'.
func (s *Service) SnapshotsOf(ctx context.Context, dataset string) ([]Snapshot, error) {
	snapshots, err := s.system.Snapshots(ctx, []string{dataset})
	if err != nil {
		return nil, toWriteError("list the snapshots of", dataset, err)
	}
	slices.SortFunc(snapshots, func(a, b Snapshot) int { return int(a.CreateTXG) - int(b.CreateTXG) })
	return snapshots, nil
}

// CreateSnapshot takes dataset@short, with every descendant at the same point
// in time when recursive.
func (s *Service) CreateSnapshot(ctx context.Context, dataset, short string, recursive bool) (SnapshotResult, error) {
	if err := checkSnapshotName(short); err != nil {
		return SnapshotResult{}, err
	}
	full := dataset + "@" + short

	existing, err := s.SnapshotsOf(ctx, dataset)
	if err != nil {
		return SnapshotResult{}, err
	}
	if slices.ContainsFunc(existing, func(snapshot Snapshot) bool { return snapshot.Name == full }) {
		return SnapshotResult{Snapshot: full, Created: []string{}, Destroyed: []string{}, Existed: true}, nil
	}

	result, createErr := s.system.CreateSnapshot(ctx, dataset, short, recursive)
	s.logWrite(ctx, OperationCreateSnapshot, result, createErr)
	if createErr != nil {
		return SnapshotResult{}, toWriteError("snapshot", full, createErr)
	}

	// Read back rather than assumed: with -r, what was actually taken depends
	// on which descendants exist, and that is worth reporting exactly.
	after, err := s.SnapshotsOf(ctx, dataset)
	if err != nil {
		return SnapshotResult{}, err
	}
	return SnapshotResult{Snapshot: full, Created: namesWithShort(after, short), Destroyed: []string{}}, nil
}

// DestroySnapshot removes dataset@short after checking that nothing depends on
// it. The check runs first so that the refusal happens before anything
// destructive does -- zfs would report the same thing, but only by failing.
func (s *Service) DestroySnapshot(ctx context.Context, dataset, short, confirm string, recursive bool) (SnapshotResult, error) {
	full := dataset + "@" + short
	if err := checkConfirm(confirm, full); err != nil {
		return SnapshotResult{}, err
	}
	snapshots, err := s.SnapshotsOf(ctx, dataset)
	if err != nil {
		return SnapshotResult{}, err
	}

	targets := []string{full}
	if recursive {
		targets = namesWithShort(snapshots, short)
	}
	if !slices.ContainsFunc(snapshots, func(snapshot Snapshot) bool { return snapshot.Name == full }) {
		return SnapshotResult{}, ErrSnapshotNotFound
	}

	block, err := s.blockersFor(ctx, targets)
	if err != nil {
		return SnapshotResult{}, err
	}
	if block != nil {
		return SnapshotResult{Snapshot: full, Created: []string{}, Destroyed: []string{}, Blocked: block}, nil
	}

	result, destroyErr := s.system.DestroySnapshot(ctx, dataset, short, recursive)
	s.logWrite(ctx, OperationDestroySnapshot, result, destroyErr)
	if destroyErr != nil {
		return SnapshotResult{}, toWriteError("destroy", full, destroyErr)
	}
	return SnapshotResult{Snapshot: full, Created: []string{}, Destroyed: targets}, nil
}

// Rollback returns dataset to dataset@short, destroying the snapshots taken
// after it only when destroyNewer says so.
//
// Rollback is never recursive: zfs rollback has no way to descend into a
// dataset's children, and its own -r flag means "destroy newer snapshots". A
// jail whose data lives in a child dataset therefore rolls its root back
// without touching that child, which is the arrangement JD-007 describes.
func (s *Service) Rollback(ctx context.Context, dataset, short, confirm string, destroyNewer bool) (SnapshotResult, error) {
	full := dataset + "@" + short
	if err := checkConfirm(confirm, full); err != nil {
		return SnapshotResult{}, err
	}
	snapshots, err := s.SnapshotsOf(ctx, dataset)
	if err != nil {
		return SnapshotResult{}, err
	}

	var target *Snapshot
	for i, snapshot := range snapshots {
		if snapshot.Name == full {
			target = &snapshots[i]
		}
	}
	if target == nil {
		return SnapshotResult{}, ErrSnapshotNotFound
	}

	// Ordered by transaction group, not by timestamp: two snapshots taken in
	// the same second tie on creation, and this is the comparison that decides
	// what gets destroyed.
	newer := make([]string, 0)
	for _, snapshot := range snapshots {
		if snapshot.Dataset == dataset && snapshot.CreateTXG > target.CreateTXG {
			newer = append(newer, snapshot.Name)
		}
	}

	if len(newer) > 0 && !destroyNewer {
		return SnapshotResult{
			Snapshot: full, Created: []string{}, Destroyed: []string{},
			Blocked: &SnapshotBlock{
				Reason: fmt.Sprintf("rolling back to %s would destroy %d newer snapshot(s); pass destroy_newer=true to accept that", full, len(newer)),
				Newer:  newer,
			},
		}, nil
	}
	// Even told to, it will not get past a clone: zfs would need -R for that,
	// which takes the clone with it and is not something this API does.
	if block, err := s.blockersFor(ctx, newer); err != nil {
		return SnapshotResult{}, err
	} else if block != nil {
		return SnapshotResult{Snapshot: full, Created: []string{}, Destroyed: []string{}, Blocked: block}, nil
	}

	result, rollbackErr := s.system.Rollback(ctx, dataset, short, destroyNewer)
	s.logWrite(ctx, OperationRollback, result, rollbackErr)
	if rollbackErr != nil {
		return SnapshotResult{}, toWriteError("roll back to", full, rollbackErr)
	}
	destroyed := newer
	if !destroyNewer {
		destroyed = []string{}
	}
	return SnapshotResult{Snapshot: full, Created: []string{}, Destroyed: destroyed}, nil
}

// blockersFor asks ZFS what depends on these snapshots, and returns nil when
// nothing does.
func (s *Service) blockersFor(ctx context.Context, snapshots []string) (*SnapshotBlock, error) {
	block := &SnapshotBlock{Clones: make([]string, 0), Holds: make([]string, 0)}
	for _, snapshot := range snapshots {
		clones, err := s.system.Clones(ctx, snapshot)
		if err != nil {
			return nil, toWriteError("read the clones of", snapshot, err)
		}
		block.Clones = append(block.Clones, clones...)

		holds, err := s.system.Holds(ctx, snapshot)
		if err != nil {
			return nil, toWriteError("read the holds on", snapshot, err)
		}
		for _, hold := range holds {
			block.Holds = append(block.Holds, hold.Snapshot+" ("+hold.Tag+")")
		}
	}
	if len(block.Clones) == 0 && len(block.Holds) == 0 {
		return nil, nil
	}

	reasons := make([]string, 0, 2)
	if len(block.Clones) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d dependent clone(s)", len(block.Clones)))
	}
	if len(block.Holds) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d hold(s)", len(block.Holds)))
	}
	block.Reason = "refused: " + strings.Join(reasons, " and ") +
		". Neither can be overridden here: releasing a hold or destroying a clone is a separate, deliberate act."
	return block, nil
}

func namesWithShort(snapshots []Snapshot, short string) []string {
	names := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.Short == short {
			names = append(names, snapshot.Name)
		}
	}
	return names
}

// checkSnapshotName applies the dataset component rules, which a snapshot name
// shares, with a message that names the right thing.
func checkSnapshotName(short string) error {
	if err := checkChildName(short); err != nil {
		var appErr *common.AppError
		if errors.As(err, &appErr) {
			return common.NewAppError(appErr.Code, strings.Replace(appErr.Message, "dataset name", "snapshot name", 1))
		}
		return err
	}
	return nil
}

// checkConfirm enforces JD-016's confirmation on every destructive call. It
// has to be the snapshot's full name -- pool, dataset and all -- so that a
// script looping over jails with one snapshot name in a variable cannot
// confirm every one of them by accident.
//
// The expected value is in the message. The confirmation guards against a
// mistake, not against a client that can already list the snapshots.
func checkConfirm(confirm, full string) error {
	if confirm == full {
		return nil
	}
	if confirm == "" {
		return common.NewAppError(http.StatusBadRequest,
			"this is destructive and needs confirm="+full)
	}
	return common.NewAppError(http.StatusBadRequest,
		"confirm="+confirm+" does not name this snapshot; it must be exactly "+full)
}
