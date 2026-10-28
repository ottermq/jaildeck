package freebsd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/ottermq/jaildeck/internal/console"
)

// consoleTerm is what a jail's shell is told its terminal is: xterm.js
// answers as one.
const consoleTerm = "xterm-256color"

// hangupGrace is how long a hung-up shell is given to go before it is killed.
// Measured, it went at once on SIGHUP (JD-042).
const hangupGrace = 2 * time.Second

// Shells starts interactive shells. It is not the command runner: a shell runs
// until someone ends it, and its output is a stream, not a capture.
type Shells struct{}

func NewShells() *Shells { return &Shells{} }

// JailShell starts `jexec -l <jail> /bin/sh -l` on a PTY. Measured on
// isengard: jexec -l passes TERM and nothing else of this environment into
// the jail, and the login shell sets its own PATH, HOME and SHELL; so TERM is
// all that is given.
func (s *Shells) JailShell(ctx context.Context, jail string, cols, rows uint16) (console.Terminal, error) {
	path, err := exec.LookPath("jexec")
	if err != nil {
		return nil, err
	}
	// Not CommandContext: the shell outlives the request that opened it, and
	// ends when the session closes it.
	cmd := exec.Command(path, "-l", jail, "/bin/sh", "-l")
	cmd.Env = []string{"TERM=" + consoleTerm}
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, fmt.Errorf("starting jexec on a PTY: %w", err)
	}
	file, err := nonBlocking(master)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	t := &ptyTerminal{file: file, cmd: cmd, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(t.exited)
	}()
	return t, nil
}

// nonBlocking swaps a PTY master for a non-blocking duplicate, which Go's
// poller manages. Measured: closing a blocking master while a read waits on
// it neither ends the read nor hangs up -- the shell, and what it was running,
// stayed alive in the jail. Closing the non-blocking duplicate ends the read
// at once and jexec gets SIGHUP.
func nonBlocking(master *os.File) (*os.File, error) {
	raw, err := master.SyscallConn()
	if err != nil {
		master.Close()
		return nil, err
	}
	dup, dupErr := -1, error(nil)
	if err := raw.Control(func(fd uintptr) { dup, dupErr = unix.Dup(int(fd)) }); err != nil {
		dupErr = err
	}
	// The original goes either way. Its Fd() would have set the descriptor
	// back to blocking, which is why the duplicate is made through Control.
	master.Close()
	if dupErr != nil {
		return nil, fmt.Errorf("duplicating the PTY master: %w", dupErr)
	}
	unix.CloseOnExec(dup)
	if err := unix.SetNonblock(dup, true); err != nil {
		unix.Close(dup)
		return nil, fmt.Errorf("making the PTY master non-blocking: %w", err)
	}
	return os.NewFile(uintptr(dup), "ptmx"), nil
}

// ptyTerminal is a shell on a PTY master.
type ptyTerminal struct {
	file   *os.File
	cmd    *exec.Cmd
	exited chan struct{}
	once   sync.Once
}

func (t *ptyTerminal) Read(p []byte) (int, error)  { return t.file.Read(p) }
func (t *ptyTerminal) Write(p []byte) (int, error) { return t.file.Write(p) }

func (t *ptyTerminal) Resize(cols, rows uint16) error {
	return pty.Setsize(t.file, &pty.Winsize{Cols: cols, Rows: rows})
}

// Close hangs up and waits for the shell to go, killing it if it has not
// within hangupGrace, so that nothing it started outlives the session.
func (t *ptyTerminal) Close() error {
	var err error
	t.once.Do(func() {
		err = t.file.Close()
		select {
		case <-t.exited:
		case <-time.After(hangupGrace):
			if killErr := t.cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				err = errors.Join(err, killErr)
			}
			<-t.exited
		}
	})
	return err
}

// VMConsole opens the host's end of a guest's nmdm pair, non-blocking so that
// Go's poller manages it and a close ends a waiting read, and makes it raw at
// once. Measured: left as it opens (echo, icanon, ixon...), the guest's output
// was echoed back to it as input -- 17 KB of newlines in 9 s, and the echoed
// text typed at its login prompt. Raw, a return got the prompt and nothing
// else. This is what vm console's cu does too.
func (s *Shells) VMConsole(_ context.Context, device string) (console.Terminal, error) {
	fd, err := unix.Open(device, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", device, err)
	}
	if _, err := term.MakeRaw(fd); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("making %s raw: %w", device, err)
	}
	return &deviceTerminal{file: os.NewFile(uintptr(fd), device)}, nil
}

// deviceTerminal is a serial console: no process behind it, and no size.
type deviceTerminal struct{ file *os.File }

func (t *deviceTerminal) Read(p []byte) (int, error)  { return t.file.Read(p) }
func (t *deviceTerminal) Write(p []byte) (int, error) { return t.file.Write(p) }
func (t *deviceTerminal) Resize(uint16, uint16) error { return nil }
func (t *deviceTerminal) Close() error                { return t.file.Close() }

var _ console.Shells = (*Shells)(nil)
