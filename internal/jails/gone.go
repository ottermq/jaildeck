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
// listed when the wait runs out. Nothing has moved when it refuses.
//
// The wait is a hard deadline (#282 review): every question to jls runs under
// it, so a slow one cannot carry the wait past its bound. It is derived from
// the caller's context, which for a re-clone is already detached from the
// request (JD-014).
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
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		gone, err := s.removal.Gone(waitCtx, name)
		if err == nil && gone {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if waitCtx.Err() != nil && ctx.Err() == nil {
			// The wait ran out, whether between questions or during one.
			return common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s is stopped but jls -d still lists it after %s: it is still being torn "+
					"down and holds its root, or it was started again. Nothing was moved; "+
					"retry once jls -d no longer lists it", name, wait))
		}
		if err != nil {
			return fmt.Errorf("ask whether %s is gone: %w", name, err)
		}
		select {
		case <-waitCtx.Done():
		case <-time.After(poll):
		}
	}
}
