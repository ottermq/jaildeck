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
