package freebsd

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
)

// execAdapter records the command it was given and answers what the test
// set up, including the error shape the real runner produces.
type execAdapter struct {
	*Adapter
	got    system.Command
	result system.CommandResult
	err    error
	calls  int
}

func newExecAdapter(t *testing.T) *execAdapter {
	t.Helper()
	e := &execAdapter{}
	e.Adapter = NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		e.calls++
		e.got = cmd
		return e.result, e.err
	}))
	return e
}

// exitError is what the runner returns for a command that ran and exited
// non-zero: the result carries the code and the error is an *exec.ExitError.
func exitError() error {
	err := exec.Command("sh", "-c", "exit 3").Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		panic("expected an ExitError")
	}
	return err
}

// --- the command line --------------------------------------------------

// -l always: without it jexec hands the jail the caller's entire
// environment, so everything the Jail Deck process was started with would be
// readable by anything run here. Measured on 15.1-RELEASE-p5.
func TestExecInJailRunsWithACleanEnvironment(t *testing.T) {
	adapter := newExecAdapter(t)

	_, err := adapter.ExecInJail(context.Background(), "db1",
		[]string{"/usr/sbin/service", "postgresql", "status"}, nil, 30*time.Second)
	if err != nil {
		t.Fatalf("ExecInJail: %v", err)
	}
	want := "jexec -l db1 /usr/sbin/service postgresql status"
	if got := adapter.got.String(); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	// No env(1) layer when there is nothing to put in it: a layer that does
	// nothing is a layer that can still go wrong.
	if strings.Contains(adapter.got.String(), "/usr/bin/env") {
		t.Error("wrapped the command in env with no variables to set")
	}
}

func TestExecInJailAppliesTheCallersVariables(t *testing.T) {
	adapter := newExecAdapter(t)

	_, err := adapter.ExecInJail(context.Background(), "db1", []string{"/bin/sh", "-c", "echo $A"},
		map[string]string{"PGDATA": "/var/db/postgres", "A": "1"}, time.Minute)
	if err != nil {
		t.Fatalf("ExecInJail: %v", err)
	}
	// Sorted, so the same request builds the same line twice running — the
	// audit log records it, and an entry that differs cannot be compared.
	want := "jexec -l db1 /usr/bin/env A=1 PGDATA=/var/db/postgres /bin/sh -c echo $A"
	if got := adapter.got.String(); got != want {
		t.Errorf("command = %q,\n want %q", got, want)
	}
}

func TestExecInJailBoundsTheCommand(t *testing.T) {
	adapter := newExecAdapter(t)

	if _, err := adapter.ExecInJail(context.Background(), "db1",
		[]string{"true"}, nil, 90*time.Second); err != nil {
		t.Fatalf("ExecInJail: %v", err)
	}
	if adapter.got.Timeout != 90*time.Second {
		t.Errorf("timeout = %s, want 90s", adapter.got.Timeout)
	}
	// The output cap is the adapter's to apply: the domain says how much,
	// the runner is what holds it.
	if adapter.got.MaxOutput != jails.MaxExecOutput {
		t.Errorf("max output = %d, want %d", adapter.got.MaxOutput, jails.MaxExecOutput)
	}
}

func TestExecInJailRefusesANameThatIsNotOne(t *testing.T) {
	for _, name := range []string{"", "db1 db2", "jail:db1", "-l"} {
		adapter := newExecAdapter(t)

		if _, err := adapter.ExecInJail(context.Background(), name,
			[]string{"true"}, nil, time.Second); err == nil {
			t.Errorf("ExecInJail accepted %q", name)
		}
		if adapter.calls != 0 {
			t.Errorf("%q reached the host", name)
		}
	}
}

// --- what it makes of the outcome --------------------------------------

func TestExecInJailReportsWhatTheCommandPrinted(t *testing.T) {
	adapter := newExecAdapter(t)
	adapter.result = system.CommandResult{Stdout: "out\n", Stderr: "err\n"}

	result, err := adapter.ExecInJail(context.Background(), "db1",
		[]string{"true"}, nil, time.Second)
	if err != nil {
		t.Fatalf("ExecInJail: %v", err)
	}
	if result.Stdout != "out\n" || result.Stderr != "err\n" {
		t.Errorf("result = %+v", result)
	}
	if result.DurationMS < 0 {
		t.Errorf("duration = %d", result.DurationMS)
	}
}

// The runner reports every non-zero exit as an error. Here the exit code is
// the answer the caller asked for, not a failure of the exec.
func TestExecInJailTreatsANonZeroExitAsTheAnswer(t *testing.T) {
	adapter := newExecAdapter(t)
	adapter.result = system.CommandResult{ExitCode: 3, Stderr: "no server running\n"}
	adapter.err = exitError()

	result, err := adapter.ExecInJail(context.Background(), "db1",
		[]string{"pg_ctl", "status"}, nil, time.Second)
	if err != nil {
		t.Fatalf("a command that exited 3 was reported as a failed exec: %v", err)
	}
	if result.ExitCode != 3 {
		t.Errorf("exit_code = %d, want 3", result.ExitCode)
	}
	if result.Stderr == "" {
		t.Error("the command's stderr was dropped")
	}
}

// jexec's own refusal looks like a command exiting 1. Reporting it as
// exit_code 1 would tell a caller their command ran and failed when it never
// started.
func TestExecInJailTellsJexecsFailureFromTheCommands(t *testing.T) {
	adapter := newExecAdapter(t)
	adapter.result = system.CommandResult{
		ExitCode: 1, Stderr: "jexec: jail \"db1\" not found\n",
	}
	adapter.err = exitError()

	result, err := adapter.ExecInJail(context.Background(), "db1",
		[]string{"true"}, nil, time.Second)
	if err == nil {
		t.Fatal("want the error: the command never ran")
	}
	if result.ExitCode != -1 {
		t.Errorf("exit_code = %d, want -1 — the command did not exit at all", result.ExitCode)
	}
}

// Narrow on purpose: a command that exits 1 with its own stderr is still the
// command's answer, even if the jail is mentioned.
func TestExecInJailDoesNotMistakeACommandForJexec(t *testing.T) {
	adapter := newExecAdapter(t)
	adapter.result = system.CommandResult{
		ExitCode: 1, Stdout: "checking\n", Stderr: "jexec: something\n",
	}
	adapter.err = exitError()

	result, err := adapter.ExecInJail(context.Background(), "db1",
		[]string{"true"}, nil, time.Second)
	if err != nil {
		t.Fatalf("a command that printed to stdout was read as jexec failing: %v", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", result.ExitCode)
	}
}

// A timed-out command is an answer too: it ran for as long as it was
// allowed, and whatever it printed first is worth returning.
func TestExecInJailReportsATimeout(t *testing.T) {
	adapter := newExecAdapter(t)
	adapter.result = system.CommandResult{ExitCode: -1, Stdout: "partial\n"}
	adapter.err = context.DeadlineExceeded

	result, err := adapter.ExecInJail(context.Background(), "db1",
		[]string{"sleep", "600"}, nil, time.Second)
	if err != nil {
		t.Fatalf("a timeout is an answer, not a failure: %v", err)
	}
	if !result.TimedOut {
		t.Error("timed_out is not set")
	}
	if result.ExitCode != -1 {
		t.Errorf("exit_code = %d, want -1", result.ExitCode)
	}
	if result.Stdout != "partial\n" {
		t.Errorf("stdout = %q, want what it printed before it was stopped", result.Stdout)
	}
}

func TestExecInJailCarriesTruncation(t *testing.T) {
	adapter := newExecAdapter(t)
	adapter.result = system.CommandResult{
		Stdout: strings.Repeat("y", 16), StdoutTruncated: true,
	}

	result, err := adapter.ExecInJail(context.Background(), "db1",
		[]string{"yes"}, nil, time.Second)
	if err != nil {
		t.Fatalf("ExecInJail: %v", err)
	}
	if !result.StdoutTruncated {
		t.Error("stdout_truncated was lost")
	}
}

// A failure that is not an exit code at all — jexec missing, the runner
// unable to start it — is the command not running.
func TestExecInJailPassesOnARealFailure(t *testing.T) {
	adapter := newExecAdapter(t)
	adapter.err = errors.New("fork/exec /usr/sbin/jexec: no such file or directory")

	result, err := adapter.ExecInJail(context.Background(), "db1",
		[]string{"true"}, nil, time.Second)
	if err == nil {
		t.Fatal("want the error")
	}
	if result.ExitCode != -1 {
		t.Errorf("exit_code = %d, want -1", result.ExitCode)
	}
}
