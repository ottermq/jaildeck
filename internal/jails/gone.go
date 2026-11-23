package jails

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
)

// JailRemoval answers whether a jail is gone from the kernel, dying ones
// included. service jail stop returns while a jail is still dying -- a vnet
// jail can stay so for as long as its TCP connections take to close -- and a
// dying jail still holds its root: zfs answers "pool or dataset is busy"
// (#281).
type JailRemoval interface {
	Gone(ctx context.Context, name string) (bool, error)
}

// SetRemoval gives the service a way to wait for a stopped jail to be gone.
func (s *JailService) SetRemoval(r JailRemoval) { s.removal = r }

const (
	// defaultGoneWait is long enough for TCP's TIME_WAIT (2 MSL, 60 s by
	// default) with room to spare.
	defaultGoneWait = 2 * time.Minute
	defaultGonePoll = time.Second
)

// waitGone returns once the jail is gone, and refuses with 409 if it is still
// there when the wait runs out. Nothing has moved when it refuses.
func (s *JailService) waitGone(ctx context.Context, name string) error {
	if s.removal == nil {
		return nil
	}
	wait, poll := s.goneWait, s.gonePoll
	if wait <= 0 {
		wait = defaultGoneWait
	}
	if poll <= 0 {
		poll = defaultGonePoll
	}
	deadline := time.Now().Add(wait)
	for {
		gone, err := s.removal.Gone(ctx, name)
		if err != nil {
			return fmt.Errorf("ask whether %s is gone: %w", name, err)
		}
		if gone {
			return nil
		}
		if time.Now().After(deadline) {
			return common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s is stopped but still dying after %s, so its root is still in use; "+
					"nothing was moved, and the jail is left stopped. Retry once jls -d no longer lists it",
				name, wait))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}
