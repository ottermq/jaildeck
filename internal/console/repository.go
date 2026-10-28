package console

import (
	"context"
	"errors"
)

// Terminal is one interactive session's far end: a shell in a jail on a PTY
// (JD-042). Read returns what the shell wrote, and an error once the shell is
// gone. Close hangs up: the shell gets SIGHUP and is waited for, and a Read
// blocked on the terminal returns at once -- which is why the PTY's master is
// non-blocking, measured.
type Terminal interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	Close() error
}

// Shells opens terminals. Kept apart from the command runner, which runs a
// command to completion with captured output (JD-014): a shell is neither.
type Shells interface {
	// JailShell starts `jexec -l <jail> /bin/sh -l` on a PTY of the given
	// size, with TERM as the only variable passed in.
	JailShell(ctx context.Context, jail string, cols, rows uint16) (Terminal, error)
}

// ErrNotRunning is a jail that is not running: there is nothing to attach to.
var ErrNotRunning = errors.New("not running")
