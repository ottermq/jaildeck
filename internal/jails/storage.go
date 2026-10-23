package jails

import (
	"context"
	"net/http"

	"github.com/ottermq/jaildeck/internal/common"
)

// ErrNoStorageWriter is returned when the application was built without a
// storage writer. Only tests do that; it is a 501 rather than a panic so that
// a misconfiguration is legible instead of fatal.
var ErrNoStorageWriter = common.NewAppError(http.StatusNotImplemented, "storage writes are not available")

// ErrJailNotOnZFS separates "no such jail" from "that jail's path is not a ZFS
// dataset" -- a jail on UFS, or one whose stanza sets no path at all. Both are
// 404, because in each case there is no dataset at the address asked for, but
// the operator reading the message needs to know which.
var ErrJailNotOnZFS = common.NewAppError(http.StatusNotFound, "this jail has no ZFS dataset of its own")

// EnsureDataset creates or updates a dataset beneath the jail's own.
func (s *JailService) EnsureDataset(ctx context.Context, name JailName, child string, properties map[string]string) (DatasetChange, error) {
	jail, err := s.Get(ctx, name)
	if err != nil {
		return DatasetChange{}, err
	}
	if s.datasetWriter == nil {
		return DatasetChange{}, ErrNoStorageWriter
	}
	if jail.Path == "" {
		return DatasetChange{}, ErrJailNotOnZFS
	}
	// A write must not be abandoned half-way because the client hung up: zfs
	// create either made the dataset or did not, and the audit entry is what
	// records which. The runner still bounds it.
	return s.datasetWriter.EnsureDataset(context.WithoutCancel(ctx), jail.Path, child, properties)
}

// SetUserProperties tags the jail's own dataset, or one beneath it when child
// is not empty.
func (s *JailService) SetUserProperties(ctx context.Context, name JailName, child string, properties map[string]*string) (map[string]string, error) {
	jail, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if s.datasetWriter == nil {
		return nil, ErrNoStorageWriter
	}
	if jail.Path == "" {
		return nil, ErrJailNotOnZFS
	}
	return s.datasetWriter.SetUserProperties(context.WithoutCancel(ctx), jail.Path, child, properties)
}

// onZFS resolves the preconditions every storage call shares: the jail exists,
// a writer is wired, and the jail is actually on ZFS.
func (s *JailService) onZFS(ctx context.Context, name JailName, writer any) (Jail, error) {
	jail, err := s.Get(ctx, name)
	if err != nil {
		return Jail{}, err
	}
	if writer == nil {
		return Jail{}, ErrNoStorageWriter
	}
	if jail.Path == "" {
		return Jail{}, ErrJailNotOnZFS
	}
	return jail, nil
}

// Snapshots lists every snapshot of the jail's datasets, its children's
// included.
func (s *JailService) Snapshots(ctx context.Context, name JailName) ([]Snapshot, error) {
	jail, err := s.onZFS(ctx, name, s.snapshots)
	if err != nil {
		return nil, err
	}
	return s.snapshots.Snapshots(ctx, jail.Path)
}

func (s *JailService) CreateSnapshot(ctx context.Context, name JailName, child, short string, recursive bool) (SnapshotResult, error) {
	jail, err := s.onZFS(ctx, name, s.snapshots)
	if err != nil {
		return SnapshotResult{}, err
	}
	return s.snapshots.CreateSnapshot(context.WithoutCancel(ctx), jail.Path, child, short, recursive)
}

func (s *JailService) DestroySnapshot(ctx context.Context, name JailName, child, short, confirm string, recursive bool) (SnapshotResult, error) {
	jail, err := s.onZFS(ctx, name, s.snapshots)
	if err != nil {
		return SnapshotResult{}, err
	}
	return s.snapshots.DestroySnapshot(context.WithoutCancel(ctx), jail.Path, child, short, confirm, recursive)
}

func (s *JailService) Rollback(ctx context.Context, name JailName, child, short, confirm string, destroyNewer bool) (SnapshotResult, error) {
	jail, err := s.onZFS(ctx, name, s.snapshots)
	if err != nil {
		return SnapshotResult{}, err
	}
	return s.snapshots.Rollback(context.WithoutCancel(ctx), jail.Path, child, short, confirm, destroyNewer)
}
