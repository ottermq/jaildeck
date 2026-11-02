package events

import (
	"context"
	"sync"
	"testing"
	"time"
)

type states struct {
	mu   sync.Mutex
	list []Instance
}

func (s *states) Instances(context.Context) ([]Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Instance(nil), s.list...), nil
}

func (s *states) set(list ...Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = list
}

func next(t *testing.T, s *Subscriber) Event {
	t.Helper()
	select {
	case e := <-s.C:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

// A change made outside Jail Deck is announced; one it announced itself is
// not announced twice; and the observer runs only while someone listens.
func TestTheObserverAnnouncesWhatChangedElsewhere(t *testing.T) {
	b := NewBroker()
	src := &states{}
	src.set(Instance{"jail", "web", "running"}, Instance{"vm", "batch", "running"})
	o := NewObserver(b, src, 20*time.Millisecond)
	defer o.Stop()
	s, _, _, _, _ := b.Subscribe("")

	time.Sleep(30 * time.Millisecond) // the first reading is the baseline
	src.set(Instance{"jail", "web", "stopped"}, Instance{"vm", "batch", "running"}, Instance{"vm", "new", "stopped"})
	got := map[string]StateChange{}
	for range 2 {
		e := next(t, s)
		c := e.Data.(StateChange)
		got[c.Kind+" "+c.Name] = c
	}
	if c := got["jail web"]; c.From != "running" || c.To != "stopped" || c.Seen != "observed" {
		t.Errorf("web = %+v", c)
	}
	if c := got["vm new"]; c.From != Absent || c.To != "stopped" {
		t.Errorf("new = %+v", c)
	}

	b.Operated("batch")
	src.set(Instance{"jail", "web", "stopped"}, Instance{"vm", "new", "stopped"})
	select {
	case e := <-s.C:
		t.Errorf("announced %+v, which Jail Deck's own operation already said", e.Data)
	case <-time.After(80 * time.Millisecond):
	}

	b.Unsubscribe(s)
	time.Sleep(30 * time.Millisecond)
	o.mu.Lock()
	running := o.cancel != nil
	o.mu.Unlock()
	if running {
		t.Error("the observer still runs with no stream open")
	}
}
