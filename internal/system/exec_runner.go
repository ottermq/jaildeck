package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
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

	stdout := newCappedBuffer(cmd.MaxOutput)
	stderr := newCappedBuffer(cmd.MaxOutput)
	c.Stdout = stdout
	c.Stderr = stderr

	start := time.Now()
	err := c.Run()
	elapsed := time.Since(start)
	result := CommandResult{
		Stdout: stdout.String(), Stderr: stderr.String(),
		StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated,
	}

	// Deferred because the branches below are what set ExitCode: logging here
	// directly would report 0 for every command, including the failures this
	// line exists to diagnose. Stdout is deliberately omitted — jls output is
	// large, and the callers that need it log it themselves.
	defer func() {
		common.Logger(ctx, "exec").Debug("command finished",
			"command", cmd.Name, "args", cmd.Args,
			"exit_code", result.ExitCode, "duration", elapsed,
			"stderr", result.Stderr)
	}()

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

// cappedBuffer keeps at most limit bytes and remembers that it dropped some.
//
// A writer rather than a truncation after the fact: a command that produces
// a gigabyte would otherwise be held in memory in full before anyone decided
// it was too much, which is the failure the cap exists to prevent. Writes
// past the limit are reported as accepted, so the command is not killed by a
// short write -- it is allowed to finish and its output is simply no longer
// being kept.
//
// The buffer is a field and not embedded, which matters more than it looks.
// exec.Cmd copies a non-*os.File writer with io.Copy, and io.Copy prefers a
// destination's ReadFrom -- which an embedded bytes.Buffer would promote,
// taking the whole stream straight past the Write below.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) String() string { return b.buf.String() }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.buf.Write(p)
	}
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.truncated = true
		if _, err := b.buf.Write(p[:room]); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}
