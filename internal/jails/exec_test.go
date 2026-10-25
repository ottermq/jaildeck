package jails

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// --- doubles -----------------------------------------------------------

type stubExecutor struct {
	result ExecResult
	err    error

	jail    string
	argv    []string
	env     map[string]string
	timeout time.Duration
	calls   int
}

func (s *stubExecutor) ExecInJail(_ context.Context, name string, argv []string,
	env map[string]string, timeout time.Duration) (ExecResult, error) {
	s.calls++
	s.jail, s.argv, s.env, s.timeout = name, argv, env, timeout
	return s.result, s.err
}

var _ JailExecutor = (*stubExecutor)(nil)

func execService(system *createSystem, executor *stubExecutor, logger *fakeLogger) *JailService {
	return NewJailService(Dependencies{System: system, Audit: logger, Exec: executor})
}

func aRunningJail() *createSystem {
	return &createSystem{existing: []Jail{{
		Name: "db1", JID: "9", Status: JailStatusRunning,
		Path: "/usr/local/jails/containers/db1",
	}}}
}

func anExec() ExecRequest {
	return ExecRequest{Argv: []string{"/usr/sbin/service", "postgresql", "status"}}
}

// --- what a caller may ask for -----------------------------------------

func TestExecRefusals(t *testing.T) {
	tests := []struct {
		name    string
		request ExecRequest
		message string
	}{
		{
			name:    "no command at all",
			request: ExecRequest{},
			message: "argv is required",
		},
		{
			name:    "a program with no name",
			request: ExecRequest{Argv: []string{"  "}},
			message: "argv[0] is empty",
		},
		{
			name:    "a timeout that is not a duration",
			request: ExecRequest{Argv: []string{"true"}, Timeout: "60"},
			message: `timeout "60" is not a duration`,
		},
		{
			name:    "a timeout of nothing",
			request: ExecRequest{Argv: []string{"true"}, Timeout: "0s"},
			message: "timeout has to be positive",
		},
		{
			// Refused rather than clamped: a command silently given less
			// time than it asked for looks like one that failed.
			name:    "a timeout past the cap",
			request: ExecRequest{Argv: []string{"true"}, Timeout: "10m"},
			message: "longer than the 5m0s this endpoint allows",
		},
		{
			name: "a variable that is not a variable name",
			request: ExecRequest{Argv: []string{"true"},
				Env: map[string]string{"not a name": "x"}},
			message: `env name "not a name" is not a variable name`,
		},
		{
			name:    "a variable name starting with a digit",
			request: ExecRequest{Argv: []string{"true"}, Env: map[string]string{"1PATH": "x"}},
			message: "is not a variable name",
		},
		{
			// env(1) has no way to end its assignments, so it would read
			// this as one more and run nothing.
			name:    "an argv[0] env would swallow",
			request: ExecRequest{Argv: []string{"FOO=bar"}, Env: map[string]string{"A": "1"}},
			message: "env(1) would read it as another assignment",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &stubExecutor{}
			service := execService(aRunningJail(), executor, &fakeLogger{})

			_, err := service.Exec(context.Background(), "db1", test.request)
			if err == nil {
				t.Fatalf("want a refusal for %+v", test.request)
			}
			if code := appCode(t, err); code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Errorf("error = %q, want it to mention %q", err, test.message)
			}
			if executor.calls != 0 {
				t.Error("ran the command anyway")
			}
		})
	}
}

// An argv[0] with an '=' is only a problem alongside env, and refusing it
// otherwise would reject a path nobody should have to rename.
func TestExecAcceptsAnEqualsInArgvWithoutEnv(t *testing.T) {
	executor := &stubExecutor{}
	service := execService(aRunningJail(), executor, &fakeLogger{})

	if _, err := service.Exec(context.Background(), "db1",
		ExecRequest{Argv: []string{"/opt/a=b/run"}}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if executor.calls != 1 {
		t.Error("the command did not run")
	}
}

func TestExecDefaultsTheTimeout(t *testing.T) {
	executor := &stubExecutor{}
	service := execService(aRunningJail(), executor, &fakeLogger{})

	result, err := service.Exec(context.Background(), "db1", anExec())
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if executor.timeout != DefaultExecTimeout {
		t.Errorf("timeout = %s, want %s", executor.timeout, DefaultExecTimeout)
	}
	// Reported back, so a caller that sent none knows what it got.
	if result.Timeout != DefaultExecTimeout.String() {
		t.Errorf("reported timeout = %q, want %q", result.Timeout, DefaultExecTimeout)
	}
}

func TestExecPassesTheCallersTimeout(t *testing.T) {
	executor := &stubExecutor{}
	service := execService(aRunningJail(), executor, &fakeLogger{})

	request := anExec()
	request.Timeout = "90s"
	if _, err := service.Exec(context.Background(), "db1", request); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if executor.timeout != 90*time.Second {
		t.Errorf("timeout = %s, want 90s", executor.timeout)
	}
}

// --- what it refuses about the jail ------------------------------------

func TestExecNeedsARunningJail(t *testing.T) {
	system := aRunningJail()
	system.existing[0].Status = JailStatusStopped
	executor := &stubExecutor{}

	_, err := execService(system, executor, &fakeLogger{}).Exec(
		context.Background(), "db1", anExec())
	if err == nil {
		t.Fatal("want a refusal for a stopped jail")
	}
	if code := appCode(t, err); code != http.StatusConflict {
		t.Errorf("status = %d, want %d", code, http.StatusConflict)
	}
	// jexec answers a stopped jail with "jail not found", which reads as
	// though it does not exist at all.
	if !strings.Contains(err.Error(), "is not running") {
		t.Errorf("error = %q, want it to say the jail is not running", err)
	}
	if executor.calls != 0 {
		t.Error("ran the command in a stopped jail")
	}
}

func TestExecUnknownJailIs404(t *testing.T) {
	_, err := execService(aRunningJail(), &stubExecutor{}, &fakeLogger{}).Exec(
		context.Background(), "nosuch", anExec())
	if !errors.Is(err, ErrJailNotFound) {
		t.Fatalf("err = %v, want ErrJailNotFound", err)
	}
}

func TestExecWithoutTheExecutorIs501(t *testing.T) {
	service := NewJailService(Dependencies{System: aRunningJail(), Audit: &fakeLogger{}})

	_, err := service.Exec(context.Background(), "db1", anExec())
	if err == nil {
		t.Fatal("want a refusal with no executor wired")
	}
	if code := appCode(t, err); code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", code, http.StatusNotImplemented)
	}
}

// --- what it reports ---------------------------------------------------

// The exec succeeded; the thing it ran failed. That distinction is the whole
// point of this endpoint, and a client reads exit_code rather than the HTTP
// status (JD-016).
func TestANonZeroExitIsNotAnError(t *testing.T) {
	executor := &stubExecutor{result: ExecResult{
		ExitCode: 1, Stderr: "pg_ctl: no server running\n", DurationMS: 12,
	}}
	service := execService(aRunningJail(), executor, &fakeLogger{})

	result, err := service.Exec(context.Background(), "db1", anExec())
	if err != nil {
		t.Fatalf("a command that exited 1 was reported as a failed exec: %v", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", result.ExitCode)
	}
	if result.Stderr == "" {
		t.Error("the command's stderr was dropped")
	}
}

func TestExecReportsWhatWasAskedFor(t *testing.T) {
	executor := &stubExecutor{result: ExecResult{Stdout: "ok\n"}}
	service := execService(aRunningJail(), executor, &fakeLogger{})

	result, err := service.Exec(context.Background(), "db1", anExec())
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Jail != "db1" {
		t.Errorf("jail = %q", result.Jail)
	}
	// The argv the caller sent, not the jexec line it became: a caller
	// reading its own log should see what it asked for.
	if strings.Join(result.Argv, " ") != strings.Join(anExec().Argv, " ") {
		t.Errorf("argv = %v, want what was sent", result.Argv)
	}
}

func TestExecCarriesTheTruncationAndTimeoutFlags(t *testing.T) {
	executor := &stubExecutor{result: ExecResult{
		ExitCode: -1, TimedOut: true, StdoutTruncated: true, Stdout: "partial",
	}}
	service := execService(aRunningJail(), executor, &fakeLogger{})

	result, err := service.Exec(context.Background(), "db1", anExec())
	if err != nil {
		t.Fatalf("a timed-out command is still an answer: %v", err)
	}
	if !result.TimedOut || !result.StdoutTruncated {
		t.Errorf("flags lost: %+v", result)
	}
	// Whatever it managed to print before it was stopped is worth having.
	if result.Stdout != "partial" {
		t.Errorf("stdout = %q", result.Stdout)
	}
}

// --- the audit log -----------------------------------------------------

// This endpoint is root in the jail, so "what did that token run" has to be
// answerable afterwards (JD-003).
func TestExecIsAudited(t *testing.T) {
	logger := &fakeLogger{}
	executor := &stubExecutor{}
	service := execService(aRunningJail(), executor, logger)

	request := anExec()
	request.Env = map[string]string{"PGDATA": "/var/db/postgres", "LANG": "C"}
	if _, err := service.Exec(context.Background(), "db1", request); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if len(logger.entries) != 1 {
		t.Fatalf("wrote %d audit entries, want 1", len(logger.entries))
	}
	entry := logger.entries[0]
	if entry.Operation != OperationExec || entry.Target != "db1" {
		t.Errorf("entry = %+v", entry)
	}
	// The line as the host ran it: -l, then the variables in a fixed order,
	// then the command. An entry that differed run to run could not be
	// compared with another.
	want := "jexec -l db1 /usr/bin/env LANG=C PGDATA=/var/db/postgres " +
		"/usr/sbin/service postgresql status"
	if entry.Command != want {
		t.Errorf("command = %q,\n want %q", entry.Command, want)
	}
}

func TestExecAuditsACommandThatCouldNotRun(t *testing.T) {
	logger := &fakeLogger{}
	executor := &stubExecutor{err: errors.New("jexec: jail not found")}
	service := execService(aRunningJail(), executor, logger)

	if _, err := service.Exec(context.Background(), "db1", anExec()); err == nil {
		t.Fatal("want the error")
	}
	if len(logger.entries) != 1 || logger.entries[0].Success {
		t.Errorf("entries = %+v, want one failed entry", logger.entries)
	}
}
