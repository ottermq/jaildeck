package system

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

type ExecCommandRunner struct{}

func NewExecCommandRunner() *ExecCommandRunner {
	return &ExecCommandRunner{}
}

// Run executes a command with the given name and arguments, capturing its output and exit code.
func (r *ExecCommandRunner) Run(ctx context.Context, cmd Command) (CommandResult, error) {

	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()

	result := CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, err
		}
		result.ExitCode = -1
		return result, err
	}

	return result, nil
}
