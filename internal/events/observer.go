package events

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
)

// Interval is how often the observer compares, while any stream is open.
// Measured on isengard: jls under 10 ms, vm list 60 ms, so this costs well
// under 1% of a core (JD-052).
const Interval = 10 * time.Second

// Absent is the state of an instance that is not there: one that appeared
// was absent, one that went is.
const Absent = "absent"

// Instance is one jail or guest and its state, as its own listing says it.
type Instance struct {
	Kind  string
	Name  string
	State string
}

// StateSource lists every jail and guest with its state. internal/app joins
// the two domains' listings; neither knows about the other (JD-010).
type StateSource interface {
	Instances(ctx context.Context) ([]Instance, error)
}

// The events' data, as the stream sends them.
type (
	Operation struct {
		Operation string    `json:"operation"`
		Target    string    `json:"target"`
		Success   bool      `json:"success"`
		RequestID string    `json:"request_id,omitempty"`
		Timestamp time.Time `json:"timestamp"`
	}
	StateChange struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		From string `json:"from"`
		To   string `json:"to"`
		// Seen is "observed": found by comparison, not caused by Jail Deck.
		Seen string `json:"seen"`
	}
	Progress struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		// Step is empty when the work is done, however it went.
		Step string `json:"step"`
	}
)

// Observer compares the instances' states every Interval while a stream is
// open, and announces what changed that Jail Deck did not announce itself:
// a `service jail stop` in a shell, a guest that powered itself off. With
// no stream open it does not run, and its last reading is dropped.
type Observer struct {
	broker   *Broker
	source   StateSource
	interval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewObserver(broker *Broker, source StateSource, interval time.Duration) *Observer {
	o := &Observer{broker: broker, source: source, interval: interval}
	broker.OnActive(o.active)
	return o
}

func (o *Observer) active(on bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if on && o.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		o.cancel, o.done = cancel, make(chan struct{})
		go o.run(ctx, o.done)
	}
	if !on && o.cancel != nil {
		o.cancel()
		o.cancel = nil
	}
}

// Stop ends the observer, for a shutdown or a test.
func (o *Observer) Stop() {
	o.mu.Lock()
	cancel, done := o.cancel, o.done
	o.cancel = nil
	o.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (o *Observer) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	previous, at := o.read(ctx), time.Now()
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		current, now := o.read(ctx), time.Now()
		if previous != nil && current != nil {
			o.announce(previous, current, at)
		}
		if current != nil {
			previous, at = current, now
		}
	}
}

func (o *Observer) read(ctx context.Context) map[[2]string]string {
	list, err := o.source.Instances(ctx)
	if err != nil {
		if ctx.Err() == nil {
			common.Logger(ctx, "events").Warn("could not compare the instances' states", slog.Any("err", err))
		}
		return nil
	}
	out := make(map[[2]string]string, len(list))
	for _, i := range list {
		out[[2]string{i.Kind, i.Name}] = i.State
	}
	return out
}

// announce publishes each difference, unless Jail Deck announced an
// operation on that name since the previous reading: the operation event
// already said it (JD-052).
func (o *Observer) announce(previous, current map[[2]string]string, since time.Time) {
	seen := map[[2]string]bool{}
	for key, to := range current {
		seen[key] = true
		from, ok := previous[key]
		if !ok {
			from = Absent
		}
		if from != to {
			o.publish(key, from, to, since)
		}
	}
	for key, from := range previous {
		if !seen[key] {
			o.publish(key, from, Absent, since)
		}
	}
}

func (o *Observer) publish(key [2]string, from, to string, since time.Time) {
	if o.broker.OperatedSince(key[1], since) {
		return
	}
	o.broker.Publish(TypeState, StateChange{Kind: key[0], Name: key[1], From: from, To: to, Seen: "observed"})
}
