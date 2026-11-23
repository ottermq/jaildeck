package storage

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/jails"
)

// CheckDestroy answers what destroying a jail's root would take with it and
// what would stop it, without touching anything.
//
// The clones are asked for rather than read out of a failed destroy, the same
// way a template's are (JD-027): zfs says "filesystem has dependent clones"
// and names one, after being asked to destroy something.
func (s *JailSource) CheckDestroy(ctx context.Context, jailPath string) (jails.RootDependents, error) {
	root, err := s.datasetFor(ctx, jailPath, "")
	if err != nil {
		return jails.RootDependents{}, err
	}
	found := jails.RootDependents{Root: root}

	children, err := s.childrenOf(ctx, root)
	if err != nil {
		return jails.RootDependents{}, err
	}
	found.Children = children

	// SnapshotsOf covers the root and everything beneath it, which is what a
	// destroy would reach.
	snapshots, err := s.service.SnapshotsOf(ctx, root)
	if err != nil {
		return jails.RootDependents{}, err
	}
	for _, snapshot := range snapshots {
		found.Snapshots = append(found.Snapshots, snapshot.Name)

		clones, err := s.service.system.Clones(ctx, snapshot.Name)
		if err != nil {
			return jails.RootDependents{}, toWriteError("read the clones of", snapshot.Name, err)
		}
		found.Clones = append(found.Clones, clones...)

		holds, err := s.service.system.Holds(ctx, snapshot.Name)
		if err != nil {
			return jails.RootDependents{}, toWriteError("read the holds on", snapshot.Name, err)
		}
		for _, hold := range holds {
			found.Holds = append(found.Holds, hold.Snapshot+" ("+hold.Tag+")")
		}
	}
	return found, nil
}

// DestroyRoot destroys the jail's own dataset and the children mounted
// beneath it (JD-007), and returns the root and the children it destroyed.
//
// One dataset at a time, deepest first, never -r and never -R (#288). zfs
// refuses a bare destroy of a dataset that has a snapshot, so one taken
// since the service checked -- by hand, or by a schedule -- stops the destroy
// at that dataset instead of going with it unreported, which is what -r did
// to a jail with children. -R would take dependent clones too, which is the
// thing every destroy here refuses to do. Bookmarks do not refuse a bare
// destroy, and go with their dataset.
//
// A destroy that fails part way stops there and returns the children already
// destroyed: nothing is undone (JD-030), and the caller reports exactly what
// is gone.
func (s *JailSource) DestroyRoot(ctx context.Context, jailPath string) (string, []string, error) {
	taken := make([]string, 0)
	root, err := s.datasetFor(ctx, jailPath, "")
	if err != nil {
		return "", taken, err
	}
	// Read again rather than carried over from CheckDestroy: a child created
	// in between is still the jail's, and would make the root's destroy fail.
	children, err := s.childrenOf(ctx, root)
	if err != nil {
		return "", taken, err
	}
	// Deepest first, so every dataset is reached after everything beneath
	// it; among equals, the reverse of zfs's order.
	slices.Reverse(children)
	slices.SortStableFunc(children, func(a, b string) int {
		return strings.Count(b, "/") - strings.Count(a, "/")
	})
	for _, child := range children {
		if err := s.destroyOne(ctx, child); err != nil {
			return root, taken, err
		}
		taken = append(taken, child)
	}
	if err := s.destroyOne(ctx, root); err != nil {
		return root, taken, err
	}
	return root, taken, nil
}

// destroyOne runs a bare zfs destroy of one dataset, audited. A snapshot or
// bookmark name is refused: zfs destroy would take it, past the refusal a
// dataset with a snapshot gives, and nothing here means to.
func (s *JailSource) destroyOne(ctx context.Context, dataset string) error {
	if strings.ContainsAny(dataset, "@#") {
		return common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"%s is not a dataset; a jail's destroy destroys only datasets", dataset))
	}
	result, err := s.service.system.DestroyDataset(ctx, dataset, false)
	s.service.logWrite(ctx, OperationDestroyDataset, result, err)
	if err != nil {
		return toWriteError("destroy", dataset, err)
	}
	return nil
}

// PurgeSnapshots destroys each snapshot of the jail's root and of everything
// beneath it whose name starts with prefix (#280).
//
// One zfs destroy per snapshot, never -r: a recursive destroy of name@snap
// takes that name on every descendant, which is right for a schedule's
// snapshots and wrong for one somebody took by hand with the same name on
// one child. Each is audited. A held or cloned snapshot makes zfs refuse,
// and that refusal is the answer -- the caller checked for both, and this
// does not check again only to lose the same race.
func (s *JailSource) PurgeSnapshots(ctx context.Context, jailPath, prefix string) ([]string, error) {
	purged := make([]string, 0)
	if prefix == "" {
		return purged, nil
	}
	root, err := s.datasetFor(ctx, jailPath, "")
	if err != nil {
		return purged, err
	}
	snapshots, err := s.service.SnapshotsOf(ctx, root)
	if err != nil {
		return purged, err
	}
	// The caller checked, but the listing is new: a snapshot without the
	// prefix taken since means the destroy will be refused anyway, and
	// purging the rest first would only lose them for nothing.
	for _, snapshot := range snapshots {
		if !strings.HasPrefix(snapshot.Short, prefix) {
			return purged, common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s does not start with %s and was taken since the destroy was checked; nothing was purged",
				snapshot.Name, prefix))
		}
	}
	for _, snapshot := range snapshots {
		result, destroyErr := s.service.system.DestroySnapshot(ctx, snapshot.Dataset, snapshot.Short, false)
		s.service.logWrite(ctx, OperationDestroySnapshot, result, destroyErr)
		if destroyErr != nil {
			return purged, toWriteError("destroy", snapshot.Name, destroyErr)
		}
		purged = append(purged, snapshot.Name)
	}
	return purged, nil
}

// childrenOf names the datasets beneath root, in the order zfs listed them.
func (s *JailSource) childrenOf(ctx context.Context, root string) ([]string, error) {
	inventory, err := s.service.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	children := make([]string, 0)
	for _, dataset := range inventory.Datasets {
		if strings.HasPrefix(dataset.Name, root+"/") {
			children = append(children, dataset.Name)
		}
	}
	return children, nil
}

var _ jails.RootDestroyer = (*JailSource)(nil)
