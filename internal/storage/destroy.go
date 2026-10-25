package storage

import (
	"context"
	"strings"

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
// beneath it (JD-007).
//
// -r only when there are children, and never -R. The difference matters at
// exactly one moment: the service has already established there are no
// snapshots, and without -r a snapshot taken in the gap since makes zfs
// refuse rather than letting -r take it along silently. -R would take
// dependent clones too, which is the thing every destroy here refuses to do.
func (s *JailSource) DestroyRoot(ctx context.Context, jailPath string) (string, error) {
	root, err := s.datasetFor(ctx, jailPath, "")
	if err != nil {
		return "", err
	}
	// Read again rather than carried over from CheckDestroy: a child created
	// in between is still the jail's, and would make a bare destroy fail.
	children, err := s.childrenOf(ctx, root)
	if err != nil {
		return "", err
	}

	result, destroyErr := s.service.system.DestroyDataset(ctx, root, len(children) > 0)
	s.service.logWrite(ctx, OperationDestroyDataset, result, destroyErr)
	if destroyErr != nil {
		return "", toWriteError("destroy", root, destroyErr)
	}
	return root, nil
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
