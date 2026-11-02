package events

import (
	"fmt"
	"testing"
	"time"
)

func TestAReconnectGetsWhatItMissed(t *testing.T) {
	b := NewBroker()
	first := b.Publish(TypeOperation, "a")
	b.Publish(TypeOperation, "b")
	b.Publish(TypeOperation, "c")
	s, replay, reset, _, err := b.Subscribe(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Unsubscribe(s)
	if reset || len(replay) != 2 || replay[0].Data != "b" || replay[1].Data != "c" {
		t.Errorf("replay = %v, reset %v", replay, reset)
	}
	// Up to date is nothing to replay and no reset.
	s2, replay, reset, _, _ := b.Subscribe(replay[1].ID)
	defer b.Unsubscribe(s2)
	if reset || len(replay) != 0 {
		t.Errorf("replay = %v, reset %v", replay, reset)
	}
}

func TestAReconnectThatCannotCatchUpIsReset(t *testing.T) {
	b := NewBroker()
	first := b.Publish(TypeOperation, 0)
	for i := 1; i <= RingSize+1; i++ {
		b.Publish(TypeOperation, i)
	}
	for name, id := range map[string]string{
		"the ring dropped it": first.ID,
		"another boot":        "00000000-1",
		"from the future":     fmt.Sprintf("%s-%d", b.boot, b.seq+5),
		"not an id":           "yesterday",
	} {
		s, replay, reset, latest, _ := b.Subscribe(id)
		b.Unsubscribe(s)
		if !reset || len(replay) != 0 || latest != fmt.Sprintf("%s-%d", b.boot, b.seq) {
			t.Errorf("%s: reset %v, replay %d, latest %s", name, reset, len(replay), latest)
		}
	}
	if len(b.ring) != RingSize {
		t.Errorf("ring holds %d", len(b.ring))
	}
}

func TestASlowStreamIsCutOffNotWaitedFor(t *testing.T) {
	b := NewBroker()
	slow, _, _, _, _ := b.Subscribe("")
	quick, _, _, _, _ := b.Subscribe("")
	defer b.Unsubscribe(quick)
	for i := 0; i < queueSize+1; i++ {
		b.Publish(TypeOperation, i)
		<-quick.C
	}
	n := 0
	for range slow.C {
		n++
	}
	if n != queueSize || b.Streams() != 1 {
		t.Errorf("slow got %d before it was closed; %d streams open", n, b.Streams())
	}
	b.Unsubscribe(slow) // already gone: nothing
}

func TestStreamsAreCappedAndListeningIsAnnounced(t *testing.T) {
	b := NewBroker()
	var told []bool
	b.OnActive(func(on bool) { told = append(told, on) })
	subs := []*Subscriber{}
	for i := 0; i < MaxStreams; i++ {
		s, _, _, _, err := b.Subscribe("")
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, s)
	}
	if _, _, _, _, err := b.Subscribe(""); err != ErrTooMany {
		t.Errorf("stream %d: %v", MaxStreams+1, err)
	}
	for _, s := range subs {
		b.Unsubscribe(s)
	}
	if fmt.Sprint(told) != "[true false]" {
		t.Errorf("told %v, want once on and once off", told)
	}
}

func TestOperatedSince(t *testing.T) {
	b := NewBroker()
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return at }
	b.Operated("web")
	if !b.OperatedSince("web", at) || b.OperatedSince("web", at.Add(time.Second)) || b.OperatedSince("db", at) {
		t.Error("OperatedSince is wrong")
	}
}
