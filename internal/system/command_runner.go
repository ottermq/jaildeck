package system

import "context"

type Command struct {
	Name string
	Args []string
}

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type CommandRunner interface {
	Run(ctx context.Context, cmd Command) (CommandResult, error)
}
