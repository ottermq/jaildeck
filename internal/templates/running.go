package templates

import (
	"sync"
	"time"
)

// Running is the one thing about a template that cannot be read off the disk:
// whether a step is executing right now, as opposed to having stopped half way
// (JD-008). A dataset without its @base snapshot looks identical either way.
type Running struct {
	// State is what the template reports while this is in flight -- "fetching"
	// rather than the "absent" the filesystem still shows.
	State string `json:"-"`
	// Step is the stage in the operator's words, not a command line.
	Step  string    `json:"step"`
	Since time.Time `json:"since"`
}

// operations records what this process is doing, by template name.
//
// Deliberately in memory and never written down. A restart kills the work, so
// a marker that survived the restart would be claiming something false --
// losing it is the truthful outcome, not a limitation, and persisting it would
// make Jail Deck own durable state about operations, which is the database
// JD-011 refuses.
//
// What *happened*, as opposed to what is happening, is the audit log's job.
type operations struct {
	mu   sync.Mutex
	work map[string]Running
}

func newOperations() *operations {
	return &operations{work: map[string]Running{}}
}

// start claims a template, and reports whether it got it. A second attempt
// while one is in flight is refused rather than queued: two processes writing
// the same dataset is not a thing to serialise, it is a thing to say no to.
func (o *operations) start(name string, running Running) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, busy := o.work[name]; busy {
		return false
	}
	running.Since = time.Now().UTC()
	o.work[name] = running
	return true
}

// step moves a claimed template to the next stage, keeping the start time: the
// operator asked for one operation and wants to know how long it has been
// going, not how long this particular command has.
func (o *operations) step(name, state, step string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if current, ok := o.work[name]; ok {
		current.State, current.Step = state, step
		o.work[name] = current
	}
}

func (o *operations) stop(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.work, name)
}

func (o *operations) of(name string) (Running, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	running, ok := o.work[name]
	return running, ok
}

// names lists every template this process is working on, so a listing can
// include one that has no dataset yet.
func (o *operations) names() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.work))
	for name := range o.work {
		out = append(out, name)
	}
	return out
}
