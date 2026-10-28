package vms

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// consoleHangups records each guest whose console was closed, and what had
// run on the host by then.
type consoleHangups struct {
	guest *guestScript
	calls []string
}

func (c *consoleHangups) HangUpVM(name string) {
	c.calls = append(c.calls, fmt.Sprintf("%s after %d commands", name, len(c.guest.ran)))
}

// Jail Deck closes a guest's console before it stops it (JD-042): a stop and a
// restart both do, a start does not.
func TestStoppingAGuestClosesItsConsoleFirst(t *testing.T) {
	g := &guestScript{states: append(runningFor(1, StateStopped), StateStopped, StateRunning, StateRunning)}
	s, _, _ := lifecycle(g)
	c := &consoleHangups{guest: g}
	s.SetConsoles(c)
	if _, err := s.Stop(context.Background(), "web"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if want := []string{"web after 0 commands"}; !slices.Equal(c.calls, want) {
		t.Errorf("hang-ups = %q, want %q: before vm stop ran", c.calls, want)
	}
	g.states, g.looks = []string{StateStopped, StateStopped, StateRunning}, 0
	if _, err := s.Start(context.Background(), "web"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(c.calls) != 1 {
		t.Errorf("a start closed a console: %q", c.calls)
	}
}
