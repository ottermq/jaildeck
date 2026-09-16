package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// commandWaitDelay bounds how long Run keeps waiting once the context has
// ended, in case child processes still hold the output pipes open.
const commandWaitDelay = 10 * time.Second

// defaultCommandTimeout applies to commands that set no Timeout of their own,
// so no command can run unbounded. A var rather than a const so tests can
// shorten it.
var defaultCommandTimeout = 10 * time.Second

type ExecCommandRunner struct{}

func NewExecCommandRunner() *ExecCommandRunner {
	return &ExecCommandRunner{}
}

func (r *ExecCommandRunner) Run(ctx context.Context, cmd Command) (CommandResult, error) {
	timeout := cmd.Timeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Signal the whole process group: service(8) is a shell script, and killing
	// only it would leave its children running and holding the pipes open.
	c.Cancel = func() error {
		return syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
	}
	c.WaitDelay = commandWaitDelay

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()

	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}

	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.ExitCode = -1
			return result, fmt.Errorf("%s timed out after %s: %w", cmd.Name, timeout, context.DeadlineExceeded)
		}
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
