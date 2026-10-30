package storage

import (
	"context"

	"github.com/ottermq/jaildeck/internal/vms"
)

// GuestSnapshots implements vms.SnapshotTaker on top of the storage service,
// here rather than in internal/vms for the reason JailSource is (JD-010).
type GuestSnapshots struct {
	service *Service
}

func NewGuestSnapshots(service *Service) *GuestSnapshots {
	return &GuestSnapshots{service: service}
}

// SnapshotDataset is the jail route's own call, recursive: the same name
// check, the same answer for a snapshot already there, the same audit line.
func (g *GuestSnapshots) SnapshotDataset(ctx context.Context, dataset, short string) (vms.SnapshotResult, error) {
	result, err := g.service.CreateSnapshot(ctx, dataset, short, true)
	if err != nil {
		return vms.SnapshotResult{}, err
	}
	return vms.SnapshotResult{Snapshot: result.Snapshot, Created: result.Created, Existed: result.Existed}, nil
}
