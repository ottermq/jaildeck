package images

import (
	"sync"
	"time"
)

// Running is the step this process is executing for an image right now: the
// one thing about it that cannot be read off the disk (JD-008). A zvol
// without @base looks the same whether tar is writing it or died an hour ago.
type Running struct {
	State string `json:"-"`
	// Source is what is being prepared, so an image with no zvol yet can
	// still say what it will be.
	Source string    `json:"source"`
	Step   string    `json:"step"`
	Since  time.Time `json:"since"`
}

// operations records what this process is doing, by image name. In memory
// and never written down, as the template domain's is: a restart kills the
// work, so a marker that survived it would be claiming something false.
type operations struct {
	mu   sync.Mutex
	work map[string]Running
}

func newOperations() *operations {
	return &operations{work: map[string]Running{}}
}

// start claims an image and reports whether it got it. A second prepare of
// the same image is refused, not queued.
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

// step moves a claimed image to its next stage, keeping the start time.
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

func (o *operations) names() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.work))
	for name := range o.work {
		out = append(out, name)
	}
	return out
}
