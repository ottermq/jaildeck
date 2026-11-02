// Package events is the live stream of what changes (#124, JD-052): Jail
// Deck's own operations as they are audited, changes made elsewhere that a
// periodic comparison sees, and the steps of long work. Events are hints: a
// client reads what one names again through the existing endpoints, which
// stay the API's contract.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The event types.
const (
	TypeOperation = "operation"
	TypeState     = "state"
	TypeProgress  = "progress"
	// TypeReset tells a client it missed events and should read everything
	// again. It is never kept in the ring.
	TypeReset = "reset"
)

const (
	// RingSize is how many events are kept for a reconnect to catch up on.
	RingSize = 256
	// MaxStreams is how many clients may listen at once.
	MaxStreams = 32
	// queueSize is how far one client may fall behind before it is cut off
	// and told to reload, rather than waited for.
	queueSize = 64
)

// ErrTooMany is a stream refused because MaxStreams are open.
var ErrTooMany = errors.New("too many event streams are open")

// Event is one entry of the stream. ID is <boot>-<sequence>: boot is random
// per process start, so an id from before a restart is never mistaken for
// one after it.
type Event struct {
	ID   string
	Type string
	Data any
	seq  uint64
}

// Subscriber is one open stream. C is closed when the subscriber fell too
// far behind; the stream then sends a reset and ends.
type Subscriber struct {
	C chan Event
}

// Broker fans events out to every open stream and keeps the last RingSize.
// In memory only: the audit log is the history, this is now (JD-011).
type Broker struct {
	mu   sync.Mutex
	boot string
	seq  uint64
	ring []Event
	subs map[*Subscriber]struct{}
	// operated is when Jail Deck last announced an operation on a target,
	// so the observer does not announce the same change a second time.
	operated map[string]time.Time
	// active is told when the first stream opens and the last one closes:
	// the observer runs only while someone listens.
	active func(bool)
	now    func() time.Time
}

func NewBroker() *Broker {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &Broker{boot: hex.EncodeToString(b), subs: map[*Subscriber]struct{}{},
		operated: map[string]time.Time{}, now: time.Now}
}

// OnActive sets what is told when listening starts and stops.
func (b *Broker) OnActive(fn func(active bool)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active = fn
}

// Publish adds an event and sends it to every stream. A stream whose queue is
// full is closed instead of waited for.
func (b *Broker) Publish(kind string, data any) Event {
	b.mu.Lock()
	b.seq++
	e := Event{ID: b.boot + "-" + strconv.FormatUint(b.seq, 10), Type: kind, Data: data, seq: b.seq}
	b.ring = append(b.ring, e)
	if len(b.ring) > RingSize {
		b.ring = b.ring[len(b.ring)-RingSize:]
	}
	var dropped []*Subscriber
	for s := range b.subs {
		select {
		case s.C <- e:
		default:
			dropped = append(dropped, s)
		}
	}
	for _, s := range dropped {
		delete(b.subs, s)
		close(s.C)
	}
	notify := b.idle(len(dropped) > 0)
	b.mu.Unlock()
	notify()
	return e
}

// Operated records that Jail Deck announced an operation on target now.
func (b *Broker) Operated(target string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.operated[target] = b.now()
}

// OperatedSince reports whether an operation on target was announced at or
// after t.
func (b *Broker) OperatedSince(target string, t time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	at, ok := b.operated[target]
	return ok && !at.Before(t)
}

// Subscribe opens a stream. lastID is the Last-Event-ID a reconnecting
// client sends: the events after it are returned to replay when the ring
// still holds them all, and reset is true when it does not -- another boot,
// an id from the future, or a gap the ring has dropped. latest is the id a
// reset carries, so the reconnect after it continues from now.
func (b *Broker) Subscribe(lastID string) (s *Subscriber, replay []Event, reset bool, latest string, err error) {
	b.mu.Lock()
	if len(b.subs) >= MaxStreams {
		b.mu.Unlock()
		return nil, nil, false, "", ErrTooMany
	}
	s = &Subscriber{C: make(chan Event, queueSize)}
	b.subs[s] = struct{}{}
	latest = b.boot + "-" + strconv.FormatUint(b.seq, 10)
	if lastID != "" {
		replay, reset = b.since(lastID)
	}
	first := len(b.subs) == 1
	active := b.active
	b.mu.Unlock()
	if first && active != nil {
		active(true)
	}
	return s, replay, reset, latest, nil
}

func (b *Broker) since(lastID string) ([]Event, bool) {
	boot, n, ok := strings.Cut(lastID, "-")
	last, err := strconv.ParseUint(n, 10, 64)
	if !ok || err != nil || boot != b.boot || last > b.seq {
		return nil, true
	}
	if last == b.seq {
		return nil, false
	}
	if len(b.ring) == 0 || b.ring[0].seq > last+1 {
		return nil, true
	}
	out := []Event{}
	for _, e := range b.ring {
		if e.seq > last {
			out = append(out, e)
		}
	}
	return out, false
}

// Unsubscribe closes a stream. Closing one already dropped does nothing.
func (b *Broker) Unsubscribe(s *Subscriber) {
	b.mu.Lock()
	_, open := b.subs[s]
	if open {
		delete(b.subs, s)
	}
	notify := b.idle(open)
	b.mu.Unlock()
	notify()
}

// idle returns what to call once the lock is let go: the active hook with
// false when the last stream just went. Called with mu held.
func (b *Broker) idle(removed bool) func() {
	if !removed || len(b.subs) > 0 || b.active == nil {
		return func() {}
	}
	active := b.active
	return func() { active(false) }
}

// Streams is how many are open.
func (b *Broker) Streams() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

func (e Event) String() string { return fmt.Sprintf("%s %s", e.ID, e.Type) }
