package system

import (
	"context"
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
}

func (c Command) String() string {
	var sb strings.Builder
	sb.WriteString(c.Name)
	for _, arg := range c.Args {
		sb.WriteString(" ")
		sb.WriteString(arg)
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
