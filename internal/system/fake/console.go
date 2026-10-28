package fake

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/ottermq/jaildeck/internal/console"
)

// Shells is a fake jail shell: it echoes what is typed, answers a line with
// the little a test or a page needs -- hostname, stty size, exit -- and ends
// when told to exit or hung up.
type Shells struct{}

func NewShells() *Shells { return &Shells{} }

func (s *Shells) JailShell(_ context.Context, jail string, cols, rows uint16) (console.Terminal, error) {
	out, in := io.Pipe()
	t := &fakeTerminal{jail: jail, cols: cols, rows: rows, out: out, in: in,
		queue: make(chan string, 1024), done: make(chan struct{})}
	// One writer, so the echo and the answers arrive in the order they were
	// made.
	go func() {
		for {
			select {
			case text := <-t.queue:
				if _, err := t.in.Write([]byte(text)); err != nil {
					return
				}
			case <-t.done:
				return
			}
		}
	}()
	t.say("FreeBSD 15.1-RELEASE (fake)\r\n\r\n" + t.prompt())
	return t, nil
}

type fakeTerminal struct {
	jail       string
	mu         sync.Mutex
	cols, rows uint16
	line       []byte
	out        *io.PipeReader
	in         *io.PipeWriter
	queue      chan string
	done       chan struct{}
	once       sync.Once
}

func (t *fakeTerminal) prompt() string { return "root@" + t.jail + ":/ # " }

func (t *fakeTerminal) say(s string) {
	select {
	case t.queue <- s:
	case <-t.done:
	}
}

func (t *fakeTerminal) Read(p []byte) (int, error) { return t.out.Read(p) }

// Write is the keyboard: each byte is echoed, and a return runs the line.
func (t *fakeTerminal) Write(p []byte) (int, error) {
	for _, b := range p {
		switch b {
		case '\r', '\n':
			t.mu.Lock()
			line := strings.TrimSpace(string(t.line))
			t.line = t.line[:0]
			cols, rows := t.cols, t.rows
			t.mu.Unlock()
			t.run(line, cols, rows)
		case 0x7f, 0x08:
			t.mu.Lock()
			if len(t.line) > 0 {
				t.line = t.line[:len(t.line)-1]
				t.say("\b \b")
			}
			t.mu.Unlock()
		default:
			t.mu.Lock()
			t.line = append(t.line, b)
			t.mu.Unlock()
			t.say(string(b))
		}
	}
	return len(p), nil
}

func (t *fakeTerminal) run(line string, cols, rows uint16) {
	switch {
	case line == "exit":
		t.say("\r\nlogout\r\n")
		_ = t.Close()
		return
	case line == "":
	case line == "hostname":
		t.say("\r\n" + t.jail)
	case line == "stty size":
		t.say(fmt.Sprintf("\r\n%d %d", rows, cols))
	default:
		t.say("\r\nsh: " + strings.Fields(line)[0] + ": not found")
	}
	t.say("\r\n" + t.prompt())
}

func (t *fakeTerminal) Resize(cols, rows uint16) error {
	t.mu.Lock()
	t.cols, t.rows = cols, rows
	t.mu.Unlock()
	return nil
}

func (t *fakeTerminal) Close() error {
	t.once.Do(func() {
		close(t.done)
		_ = t.in.Close()
	})
	return nil
}

var _ console.Shells = (*Shells)(nil)
