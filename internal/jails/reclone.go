package jails

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ottermq/jaildeck/internal/common"
)

// Reclone replaces a jail's root with a fresh clone of a template snapshot,
// keeping its child datasets: the upgrade path for a jail whose state lives in
// a child (JD-007, #6).
//
// stop, move the children aside, park the old root, clone the new one, move the
// children back, start. The jail ends in the state it was found in.
//
// Nothing is destroyed, at any step. If one fails, the children are left where
// they were put and the result says exactly where that is — a second operation
// running automatically while the first one's failure is still unexplained is
// how a bad situation becomes an unreadable one (JD-026).
func (s *JailService) Reclone(ctx context.Context, name JailName, templateSnapshot, confirm string) (Reclone, error) {
	if confirm != name.String() {
		return Reclone{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"this replaces %s's root filesystem and stops it to do so; it needs confirm=%s", name, name))
	}
	jail, err := s.onZFS(ctx, name, s.recloner)
	if err != nil {
		return Reclone{}, err
	}

	// Everything that can be refused is refused before the jail is stopped. A
	// template name with a typo in it should cost an error message, not an
	// outage.
	if err := s.recloner.CheckReclone(ctx, jail.Path, templateSnapshot); err != nil {
		return Reclone{}, err
	}

	// Detached from request cancellation like every other mutation: a client
	// hanging up in the middle of this must not leave it half done (JD-014).
	opCtx := context.WithoutCancel(ctx)

	wasRunning := jail.Status == JailStatusRunning
	if wasRunning {
		if _, err := s.Stop(opCtx, name); err != nil {
			return Reclone{FailedAt: "stop", Running: true}, err
		}
	}

	result, recloneErr := s.recloner.Reclone(opCtx, jail.Path, templateSnapshot)
	if recloneErr != nil {
		// Left stopped on purpose. Starting a jail whose root may be half
		// replaced is not a recovery, and the result says where its data is.
		result.Running = false
		return result, recloneErr
	}

	if wasRunning {
		if _, err := s.Start(opCtx, name); err != nil {
			// The datasets are all where they belong; only the jail is down.
			result.FailedAt = "start"
			return result, err
		}
		result.Running = true
	}
	return result, nil
}
