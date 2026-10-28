package jails

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
)

// OperationExec is the audit entry for a command run inside a jail. Every
// one is logged with the command line it became, because this endpoint is
// root in the jail and "what did that token do" has to be answerable
// afterwards (JD-003, JD-016).
const OperationExec = "exec"

// What a caller may ask for, and what they may not (JD-014).
const (
	// DefaultExecTimeout is what an exec with no timeout of its own gets,
	// and is the figure JD-016's own example uses.
	DefaultExecTimeout = 60 * time.Second
	// MaxExecTimeout is the ceiling. A caller can shorten the bound and
	// never remove it: the work exec is for is an in-jail bootstrap, which
	// is seconds, and the minutes-long work is building a template, which
	// is detached and polled rather than held open (JD-008).
	MaxExecTimeout = 5 * time.Minute
	// MaxExecOutput is how much of each stream is kept. Measured in a real
	// jail: `ps auxww` is 1.4 KB and `find / -type f` is 2.85 MB, which is
	// the shape of the mistake this is for.
	MaxExecOutput = 1 << 20
)

// ExecRequest is the body of POST /api/jails/{name}/exec.
type ExecRequest struct {
	// Argv is the command and its arguments. An array, never a string:
	// there is no shell here to split it, and nothing for a quote or a
	// semicolon in an argument to mean. A caller who wants a shell asks for
	// one -- ["sh", "-c", "..."] -- and has chosen it deliberately.
	Argv []string `json:"argv"`
	// Timeout is a Go duration: "30s", "2m". Absent means 60s; longer than
	// five minutes is refused rather than clamped, because silently running
	// for less time than asked is how a caller concludes their command
	// failed when it was cut off.
	Timeout string `json:"timeout,omitempty"`
	// Env are variables for the command, on top of the clean login
	// environment jexec -l gives it. The host's own environment is never
	// passed in: measured, jexec hands the jail everything the Jail Deck
	// process was started with, jaildeck.env included (JD-016).
	Env map[string]string `json:"env,omitempty"`
}

// ExecResult is what running it did.
type ExecResult struct {
	Jail string `json:"jail"`
	// Argv as it was asked for, so a caller reading a log sees what it sent
	// rather than the jexec line it became.
	Argv []string `json:"argv"`
	// ExitCode is the command's own. **A non-zero one is still a 200**: the
	// exec succeeded and the thing it ran failed, and that distinction is
	// the whole point of this endpoint (JD-016). It is -1 when the command
	// never ran or was stopped by the timeout.
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	// Set when a stream was cut off at the cap. Reported rather than left to
	// be inferred from a length, because output ending exactly at the cap is
	// indistinguishable from output that happened to end there.
	StdoutTruncated bool `json:"stdout_truncated,omitempty"`
	StderrTruncated bool `json:"stderr_truncated,omitempty"`
	// TimedOut says the command was stopped rather than finished. The
	// process was killed inside the jail -- jexec does not fork, so the
	// process is the command -- along with anything it had forked.
	TimedOut   bool   `json:"timed_out,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Timeout    string `json:"timeout"`
}

// envNameRE is what a variable may be called. The value is not inspected:
// it is the caller's string and Jail Deck is a courier for it, the same
// position JD-023 takes for a ZFS user property.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Exec runs a command inside a running jail and reports what it did
// (JD-016).
//
// This is root inside the jail, which is no more than the API already
// implies (JD-001) -- it is JD-007's "hand off to in-jail provisioning"
// made callable. Every call is audited with the command line it became.
func (s *JailService) Exec(ctx context.Context, name JailName, request ExecRequest) (ExecResult, error) {
	if s.exec == nil {
		return ExecResult{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck cannot run commands inside a jail")
	}
	argv, env, timeout, err := normalizeExec(request)
	if err != nil {
		return ExecResult{}, err
	}

	jail, err := s.Get(ctx, name)
	if err != nil {
		return ExecResult{}, err
	}
	if jail.Status != JailStatusRunning {
		// jexec answers a stopped jail with "jail not found", which reads as
		// though the jail does not exist at all.
		return ExecResult{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is not running, and a command can only be run inside a jail that is", name))
	}

	// Detached from the request like every other mutation: a client hanging
	// up must not abandon a command half way through (JD-014). The timeout
	// still bounds it, and it is the caller's own.
	opCtx := context.WithoutCancel(ctx)
	result, err := s.exec.ExecInJail(opCtx, name.String(), argv, env, timeout)
	result.Jail, result.Argv, result.Timeout = name.String(), request.Argv, timeout.String()

	s.logExec(opCtx, name.String(), execCommandLine(name.String(), argv, env), result, err)
	if err != nil {
		return result, err
	}
	return result, nil
}

// normalizeExec refuses what this domain can judge before anything runs.
func normalizeExec(request ExecRequest) ([]string, map[string]string, time.Duration, error) {
	if len(request.Argv) == 0 {
		return nil, nil, 0, badSpec("argv is required: it is the command to run and its arguments")
	}
	if strings.TrimSpace(request.Argv[0]) == "" {
		return nil, nil, 0, badSpec("argv[0] is empty: it has to name the program to run")
	}

	timeout := DefaultExecTimeout
	if request.Timeout != "" {
		parsed, err := time.ParseDuration(request.Timeout)
		if err != nil {
			return nil, nil, 0, badSpec(fmt.Sprintf(
				"timeout %q is not a duration; it has to be like \"30s\" or \"2m\"", request.Timeout))
		}
		if parsed <= 0 {
			return nil, nil, 0, badSpec("timeout has to be positive; leave it out for " +
				DefaultExecTimeout.String())
		}
		// Refused rather than clamped: a command silently given less time
		// than asked for looks to its caller like one that failed.
		if parsed > MaxExecTimeout {
			return nil, nil, 0, badSpec(fmt.Sprintf(
				"timeout %s is longer than the %s this endpoint allows. Work that takes longer "+
					"belongs in a template, which is prepared in the background and polled (JD-008)",
				parsed, MaxExecTimeout))
		}
		timeout = parsed
	}

	for _, name := range sortedEnvNames(request.Env) {
		if !envNameRE.MatchString(name) {
			return nil, nil, 0, badSpec(fmt.Sprintf(
				"env name %q is not a variable name: letters, digits and underscores, "+
					"not starting with a digit", name))
		}
	}
	// `env` has no way to end its assignments -- measured, it answers `--`
	// with "No such file or directory" -- so it reads any leading argument
	// holding an '=' as one more of them. The command would quietly become
	// `env` printing the environment instead of the program asked for.
	if len(request.Env) > 0 && strings.Contains(request.Argv[0], "=") {
		return nil, nil, 0, badSpec(fmt.Sprintf(
			"argv[0] %q holds an '=' and this request sets environment variables. "+
				"env(1) would read it as another assignment and run nothing. Set the variable "+
				"inside the command instead, or drop env", request.Argv[0]))
	}
	return request.Argv, request.Env, timeout, nil
}

func sortedEnvNames(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// execCommandLine is what the audit log records: the line this became, not
// the argv it came from. An operator asking "what did that token run" wants
// the command as the host saw it.
//
// Values are included. They are the caller's own strings and Jail Deck does
// not read them -- but it is worth saying plainly that an exec which passes
// a secret in an environment variable has written it to the audit log, and
// that the log is 0700 and root-owned for exactly that kind of reason.
func execCommandLine(name string, argv []string, env map[string]string) string {
	parts := []string{"jexec", "-l", name}
	if len(env) > 0 {
		parts = append(parts, "/usr/bin/env")
		for _, variable := range sortedEnvNames(env) {
			parts = append(parts, variable+"="+env[variable])
		}
	}
	return strings.Join(append(parts, argv...), " ")
}

// logExec audits an exec. A command that could not be run is a failed entry,
// as for any step. One that ran is a successful exec whatever it exited with
// (JD-016), and its entry carries the command's own exit code, whether it was
// stopped by the timeout, and how long it took (#141): "what did that token
// run, and what came of it" is what the entry is for.
func (s *JailService) logExec(ctx context.Context, target, command string, result ExecResult, err error) {
	if err != nil {
		s.logStep(ctx, OperationExec, target, command, err)
		return
	}
	entry := audit.Entry{
		Timestamp:  time.Now(),
		Operation:  OperationExec,
		Target:     target,
		Command:    command,
		Success:    true,
		ExitCode:   result.ExitCode,
		TimedOut:   result.TimedOut,
		DurationMS: result.DurationMS,
	}
	if logErr := s.operationLogger.Log(ctx, entry); logErr != nil {
		common.Logger(ctx, "jail").Error("failed to write operation log",
			slog.String("operation", OperationExec), slog.String("target", target), slog.Any("err", logErr))
	}
}
