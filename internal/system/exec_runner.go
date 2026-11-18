package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

	if cmd.Into != nil {
		return r.runPipe(ctx, cmd, timeout)
	}
	c := prepare(ctx, cmd)
	stdout := newCappedBuffer(cmd.MaxOutput)
	stderr := newCappedBuffer(cmd.MaxOutput)
	c.Stdout = stdout
	c.Stderr = stderr
	if cmd.StdoutFile != "" {
		// An *os.File is handed to the command as its descriptor, so what it
		// writes goes straight to the file rather than through a pipe here.
		out, err := os.OpenFile(cmd.StdoutFile, os.O_WRONLY, 0)
		if err != nil {
			return CommandResult{ExitCode: -1}, fmt.Errorf("opening %s for the output of %s: %w", cmd.StdoutFile, cmd.Name, err)
		}
		defer out.Close()
		c.Stdout = out
	}

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
		if errors.Is(err, exec.ErrNotFound) {
			// Name the PATH it looked on. "smartctl: not found" sends an
			// operator to install a package they already have.
			return result, fmt.Errorf("%w: %s is not on PATH %s: %w",
				ErrCommandNotFound, cmd.Name, os.Getenv("PATH"), err)
		}
		return result, err
	}

	return result, nil
}

// prepare builds the process for one command: its own process group, the
// timeout's signal, and the credential it runs as.
func prepare(ctx context.Context, cmd Command) *exec.Cmd {
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Signal the whole process group: service(8) is a shell script, and killing
	// only it would leave its children running and holding the pipes open.
	c.Cancel = func() error {
		return syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
	}
	if cmd.Detach {
		// A new session, so nothing it leaves behind shares a process group
		// or a session with Jail Deck, and a timeout that signals the
		// command alone. What it leaves behind is what it was run for.
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		c.Cancel = func() error {
			return c.Process.Signal(syscall.SIGTERM)
		}
	}
	if cmd.Credential != nil {
		// NoSetGroups off, so the child's groups are set to cred.Groups --
		// none when empty -- and the root process's own, wheel and
		// operator, are dropped rather than inherited.
		cred := *cmd.Credential
		cred.NoSetGroups = false
		c.SysProcAttr.Credential = &cred
	}
	c.WaitDelay = commandWaitDelay
	return c
}

// ErrBadPipe is a Command whose Into asks for what a pipe cannot do.
var ErrBadPipe = errors.New("a piped command may not detach, write to a file or pipe on")

// runPipe runs cmd with its standard output feeding cmd.Into's standard
// input, the two joined by an OS pipe and nothing passing through Jail Deck.
// The result is the second command's output, and both commands' standard
// error, each prefixed with its command's name. The first to fail is the
// error: when the writer fails, the reader sees a short stream and fails
// too, and the writer's failure is the cause.
func (r *ExecCommandRunner) runPipe(ctx context.Context, cmd Command, timeout time.Duration) (CommandResult, error) {
	into := *cmd.Into
	if cmd.Detach || cmd.StdoutFile != "" || into.Detach || into.StdoutFile != "" || into.Into != nil {
		return CommandResult{ExitCode: -1}, ErrBadPipe
	}

	writer, reader := prepare(ctx, cmd), prepare(ctx, into)
	pr, pw, err := os.Pipe()
	if err != nil {
		return CommandResult{ExitCode: -1}, fmt.Errorf("making the pipe for %s: %w", cmd, err)
	}
	writerErr := newCappedBuffer(cmd.MaxOutput)
	readerOut, readerErr := newCappedBuffer(into.MaxOutput), newCappedBuffer(into.MaxOutput)
	writer.Stdout, writer.Stderr = pw, writerErr
	reader.Stdin, reader.Stdout, reader.Stderr = pr, readerOut, readerErr

	start := time.Now()
	if err := reader.Start(); err != nil {
		pr.Close()
		pw.Close()
		return CommandResult{ExitCode: -1}, pipeStartError(into, err)
	}
	if err := writer.Start(); err != nil {
		pr.Close()
		pw.Close()
		// The reader sees end of input and ends on its own.
		_ = reader.Wait()
		return CommandResult{ExitCode: -1}, pipeStartError(cmd, err)
	}
	// Only the children hold the pipe now; with these open, the reader
	// would never see the end of its input.
	pr.Close()
	pw.Close()

	errWrite := writer.Wait()
	errRead := reader.Wait()
	elapsed := time.Since(start)

	result := CommandResult{
		Stdout:          readerOut.String(),
		Stderr:          prefixed(label(cmd), writerErr.String()) + prefixed(label(into), readerErr.String()),
		StdoutTruncated: readerOut.truncated,
		StderrTruncated: writerErr.truncated || readerErr.truncated,
	}
	common.Logger(ctx, "exec").Debug("pipe finished",
		"command", cmd.String(), "duration", elapsed, "stderr", result.Stderr)

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		return result, fmt.Errorf("%s timed out after %s: %w", cmd, timeout, context.DeadlineExceeded)
	}
	// Which half to blame. A writer that failed leaves the reader a short
	// stream, so the writer is the cause -- unless it failed because the
	// reader had already gone, killed by SIGPIPE or reporting a broken pipe.
	order := []struct {
		name string
		err  error
	}{{label(cmd), errWrite}, {label(into), errRead}}
	if errRead != nil && brokenPipe(errWrite, writerErr.String()) {
		order[0], order[1] = order[1], order[0]
	}
	for _, failed := range order {
		if failed.err == nil {
			continue
		}
		result.ExitCode = -1
		var exitErr *exec.ExitError
		if errors.As(failed.err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		}
		return result, fmt.Errorf("%s: %w", failed.name, failed.err)
	}
	return result, nil
}

// brokenPipe answers whether a writer failed for want of a reader.
func brokenPipe(err error, stderr string) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGPIPE {
		return true
	}
	return strings.Contains(strings.ToLower(stderr), "broken pipe")
}

func pipeStartError(cmd Command, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w: %s is not on PATH %s: %w", ErrCommandNotFound, cmd.Name, os.Getenv("PATH"), err)
	}
	return fmt.Errorf("starting %s: %w", cmd.Name, err)
}

// label names one half of a pipe: the command and its first argument, which
// for zfs is the subcommand -- "zfs send", "zfs receive" -- where the name
// alone is the same on both sides.
func label(cmd Command) string {
	if len(cmd.Args) > 0 {
		return cmd.Name + " " + cmd.Args[0]
	}
	return cmd.Name
}

// prefixed labels each line of a command's standard error with its name, so
// the two halves of a pipe can be told apart in one string.
func prefixed(name, stderr string) string {
	if stderr == "" {
		return ""
	}
	var sb strings.Builder
	for _, line := range strings.SplitAfter(stderr, "\n") {
		if line == "" {
			continue
		}
		sb.WriteString(name + ": " + line)
		if !strings.HasSuffix(line, "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String()
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
