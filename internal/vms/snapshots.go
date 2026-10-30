package vms

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ottermq/jaildeck/internal/common"
)

// Consistency is what a guest snapshot can be trusted to hold (JD-045).
type Consistency string

const (
	// Taken of a stopped guest: everything it wrote is on its disks.
	Consistent Consistency = "consistent"
	// Taken while bhyve ran the guest: its disks as a power cut would have
	// left them, without what was still in the guest's memory. Enough to go
	// back to before a change; not a backup to trust as one.
	CrashConsistent Consistency = "crash-consistent"
)

// SnapshotResult is what a guest snapshot did.
type SnapshotResult struct {
	// dataset@short, the guest's own.
	Snapshot string `json:"snapshot"`
	// Every snapshot taken: the guest's dataset and each disk volume.
	Created []string `json:"created"`
	// The snapshot was already there and nothing ran (JD-016). Its
	// consistency is then unknown, and left out.
	Existed bool `json:"existed,omitempty"`
	// From the guest's state read just before the snapshot.
	Consistency Consistency `json:"consistency,omitempty"`
}

// SetSnapshots is set after construction, as the labels are (JD-045).
func (s *Service) SetSnapshots(taker SnapshotTaker) { s.snapshots = taker }

// CreateSnapshot snapshots a guest's dataset and its disks at one point in
// time, running or not: zfs snapshot -r on <datastore's dataset>/<name>,
// which is what vm snapshot -f runs (measured, JD-045). vm-bhyve refuses a
// running guest without -f; Jail Deck takes it and says it is
// crash-consistent, which is the decision.
func (s *Service) CreateSnapshot(ctx context.Context, name, short string) (SnapshotResult, error) {
	if s.system == nil {
		return SnapshotResult{}, s.unavailable()
	}
	if s.snapshots == nil {
		return SnapshotResult{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck is running without the storage access it needs to snapshot a guest")
	}
	guest, err := s.guest(ctx, name)
	if err != nil {
		return SnapshotResult{}, err
	}
	stores, err := s.system.VMDatasets(ctx)
	if err != nil {
		return SnapshotResult{}, readError("vm-bhyve's datastores", err)
	}
	dataset := guestDataset(stores, *guest)
	if dataset == "" {
		return SnapshotResult{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is in the datastore %s, which is not on ZFS, so it has no dataset to snapshot",
			name, guest.Datastore))
	}
	// A write is not abandoned because the client hung up (JD-014).
	result, err := s.snapshots.SnapshotDataset(context.WithoutCancel(ctx), dataset, short)
	if err != nil {
		return SnapshotResult{}, err
	}
	if !result.Existed {
		result.Consistency = CrashConsistent
		if guest.State == StateStopped {
			result.Consistency = Consistent
		}
	}
	return result, nil
}
