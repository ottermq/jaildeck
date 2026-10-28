package console

import "time"

// SetHeartbeat shortens how often a session looks at its idleness and its
// login, so that the tests need not wait thirty seconds.
func SetHeartbeat(h *Handler, d time.Duration) { h.tick = d }

// SetGuestWatch shortens how often a VM session looks at its guest.
func SetGuestWatch(s *Service, d time.Duration) { s.watch = d }
