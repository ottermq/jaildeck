package freebsd

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
)

// ExecInJail runs a command inside a running jail.
//
//	jexec -l <name> [/usr/bin/env K=V ...] argv...
//
// **-l always.** Measured on 15.1-RELEASE-p5: without it `jexec` hands the
// jail the caller's entire environment, so everything the Jail Deck process
// was started with -- jaildeck.env included -- would be readable by anything
// run through this endpoint. `-l` replaces it with a clean login
// environment: HOME, PATH, SHELL, USER, LANG, and nothing of the host's
// (JD-016).
//
// The caller's own variables go on top with env(1), and only when there are
// any: a layer that does nothing is a layer that can still go wrong. env has
// no way to end its assignments -- measured, `env -- cmd` fails -- which is
// why the domain refuses an argv[0] holding an '=' alongside them.
//
// A command that ran and exited non-zero is not an error: its exit code is
// the answer, and the caller reads it rather than the HTTP status.
func (a *Adapter) ExecInJail(ctx context.Context, name string, argv []string,
	env map[string]string, timeout time.Duration) (jails.ExecResult, error) {

	if err := checkJailName(name); err != nil {
		return jails.ExecResult{}, err
	}
	args := []string{"-l", name}
	if len(env) > 0 {
		args = append(args, "/usr/bin/env")
		for _, variable := range sortedKeys(env) {
			args = append(args, variable+"="+env[variable])
		}
	}
	args = append(args, argv...)

	cmd := system.Command{
		Name:      "jexec",
		Args:      args,
		Timeout:   timeout,
		MaxOutput: jails.MaxExecOutput,
	}

	started := time.Now()
	result, err := a.runner.Run(ctx, cmd)
	elapsed := time.Since(started)

	out := jails.ExecResult{
		ExitCode:        result.ExitCode,
		Stdout:          result.Stdout,
		Stderr:          result.Stderr,
		StdoutTruncated: result.StdoutTruncated,
		StderrTruncated: result.StderrTruncated,
		DurationMS:      elapsed.Milliseconds(),
	}
	if err == nil {
		return out, nil
	}

	// The timeout is the one failure that is still an answer: the command
	// ran, for as long as it was allowed, and whatever it managed to print
	// is worth returning. jexec does not fork -- the process is the command,
	// inside the jail -- so killing it kills the command, and the runner
	// signals the whole group so anything it forked goes too.
	if errors.Is(err, context.DeadlineExceeded) {
		out.TimedOut = true
		out.ExitCode = -1
		return out, nil
	}

	// A command that ran and exited non-zero is the ordinary case, not a
	// failure: the runner reports every non-zero exit as an error, and here
	// the exit code is the answer the caller asked for.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && !jexecFailed(result) {
		return out, nil
	}

	// Anything else means the command could not be run at all. The exit code
	// a failed jexec carries is jexec's own, not the command's, so it is not
	// reported as one.
	out.ExitCode = -1
	return out, err
}

// jexecFailed tells jexec's own refusal from the command's exit code.
//
// They look alike: `jexec <gone> cmd` exits 1 with "jail not found" on
// stderr -- measured -- and so could a command. Reporting that as `exit_code
// 1` would tell a caller their command ran and failed when it never started.
//
// Narrow on purpose: exit 1, nothing at all on stdout, and a stderr that
// begins the way jexec's messages do. A command that manages all three is
// reported as a failure to run; the service checks the jail is running
// first, so this only answers the race where it stopped in between.
func jexecFailed(result system.CommandResult) bool {
	return result.ExitCode == 1 && result.Stdout == "" &&
		strings.HasPrefix(strings.TrimSpace(result.Stderr), "jexec:")
}

// sortedKeys orders the variables so the command line is the same for the
// same request -- the audit log records it, and an entry that differs run to
// run cannot be compared with another.
func sortedKeys(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

var _ jails.JailExecutor = (*Adapter)(nil)
