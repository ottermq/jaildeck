package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Command struct {
	Name string
	Args []string
	// Timeout bounds this command. Zero means the runner's default.
	Timeout time.Duration
	// MaxOutput caps how much of each stream is kept, in bytes. Zero keeps
	// everything, which is what every command Jail Deck composes itself
	// wants: their output is bounded by what they are.
	//
	// It exists for exec, where the command is the caller's and its output
	// is whatever that produces -- measured, `find / -type f` in a jail is
	// 2.85 MB (JD-014). A cap here rather than a truncation afterwards,
	// because the point is not to return less but to never hold it.
	MaxOutput int
	// Detach runs the command in a session of its own, and a timeout signals
	// only the command, never its process group. For a command that leaves a
	// process running on purpose: `vm start` backgrounds vm-bhyve's
	// supervisor into its own process group, measured, and killing that
	// group would power the guest off (JD-019, JD-036).
	Detach bool
	// StdoutFile sends the command's standard output into this file instead
	// of keeping it, which leaves Stdout empty. The file must already exist:
	// it is opened for writing and never created or truncated. For a command
	// whose output is the point, and too large to hold -- `tar -xO` writing a
	// VM image's disk into its zvol (JD-037). The command writes to it
	// directly, with no copy through Jail Deck.
	StdoutFile string
}

func (c Command) String() string {
	var sb strings.Builder
	sb.WriteString(c.Name)
	for _, arg := range c.Args {
		sb.WriteString(" ")
		sb.WriteString(arg)
	}
	if c.StdoutFile != "" {
		sb.WriteString(" > ")
		sb.WriteString(c.StdoutFile)
	}
	return sb.String()
}

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// Set when MaxOutput cut that stream short. Reported rather than left to
	// be inferred from a length: output that stops at exactly the cap is
	// indistinguishable from output that happened to end there.
	StdoutTruncated bool
	StderrTruncated bool
}

// ErrCommandNotFound is a command that never ran because no executable by that
// name is on this process's PATH. Kept apart from a command that ran and
// failed: "not installed" and "installed where this process does not look"
// are different answers, and treating them as one hid disk health from the
// packaged service for two releases -- rc runs services with
// PATH=/sbin:/bin:/usr/sbin:/usr/bin, and smartctl lives in /usr/local/sbin.
var ErrCommandNotFound = errors.New("command not found")

type CommandRunner interface {
	Run(ctx context.Context, cmd Command) (CommandResult, error)
}

type CommandError struct {
	Command string
	Args    []string
	Result  CommandResult
	Err     error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("'%s %v' failed: %v", e.Command, strings.Join(e.Args, " "), e.Err)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func (e *CommandError) Summary() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}
