package jails

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// hangups records which jails' consoles were hung up, and what the system had
// been asked to do by then.
type hangups struct {
	system *createSystem
	calls  []string
}

func (h *hangups) HangUpJail(name string) {
	h.calls = append(h.calls, name+" before "+h.system.lastOperation())
}

// lastOperation is what the system had done by then: its starts and stops.
func (s *createSystem) lastOperation() string {
	return fmt.Sprintf("%d stops, %d starts", len(s.stopped), len(s.started))
}

// A jail's consoles are hung up before it stops or restarts -- an open shell
// made a stop take 10 s (JD-042) -- and not when it starts.
func TestStopAndRestartHangUpTheConsolesFirst(t *testing.T) {
	system := aRunningJail()
	service := NewJailService(Dependencies{System: system, Audit: &fakeLogger{}})
	h := &hangups{system: system}
	service.SetConsoles(h)
	ctx := context.Background()
	name, _ := NewJailName("db1")

	_, _ = service.Stop(ctx, name)
	_, _ = service.Start(ctx, name)
	_, _ = service.Restart(ctx, name)
	// Restart is one system call of its own here, so the second hang-up
	// comes after the one stop and the one start.
	if want := []string{"db1 before 0 stops, 0 starts", "db1 before 1 stops, 1 starts"}; !slices.Equal(h.calls, want) {
		t.Errorf("hang-ups = %q, want %q", h.calls, want)
	}
}
