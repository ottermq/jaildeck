package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
)

func TestExecCommandRunner_Run_KillsChildrenOnTimeout(t *testing.T) {
	cmd := Command{
		Name:    "sh",
		Args:    []string{"-c", "sleep 30 & wait"},
		Timeout: 100 * time.Millisecond,
	}
	start := time.Now()
	_, err := (&ExecCommandRunner{}).Run(context.Background(), cmd)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want timeout, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Run blocked for %s - children kept the pipes open", time.Since(start))
	}
}

// The default is a package-level var precisely so this test can shrink it;
// waiting out the real 10s default would not be a unit test.
func TestExecCommandRunner_Run_AppliesDefaultTimeout(t *testing.T) {
	old := defaultCommandTimeout
	t.Cleanup(func() { defaultCommandTimeout = old })
	defaultCommandTimeout = 100 * time.Millisecond

	// No Timeout of its own, so the default has to bound it.
	cmd := Command{Name: "sleep", Args: []string{"30"}}

	start := time.Now()
	_, err := NewExecCommandRunner().Run(context.Background(), cmd)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want the default to time it out, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Run blocked for %s; the default was not applied", elapsed)
	}
}

func TestExecCommandRunner_Run_Succeeds(t *testing.T) {
	cmd := Command{Name: "sh", Args: []string{"-c", "printf out; printf err >&2"}}

	result, err := NewExecCommandRunner().Run(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "out" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "out")
	}
	if result.Stderr != "err" {
		t.Errorf("Stderr = %q, want %q", result.Stderr, "err")
	}
}

// A client that disconnects must not be reported as a timeout: one is the
// operator's fault, the other is ours, and the audit trail should tell them
// apart.
func TestExecCommandRunner_Run_CancelledParentIsNotATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := Command{Name: "sleep", Args: []string{"30"}}

	_, err := NewExecCommandRunner().Run(ctx, cmd)
	if err == nil {
		t.Fatal("Run() returned nil error for a cancelled context")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cancellation reported as a timeout: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestExecCommandRunner_Run_LogsToContextLogger(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := common.ContextWithLogger(context.Background(), logger)

	result, err := NewExecCommandRunner().Run(ctx, Command{Name: "true"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}

	var records []map[string]any
	decoder := json.NewDecoder(&logs)
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode log record: %v", err)
		}
		records = append(records, record)
	}
	if len(records) != 1 {
		t.Fatalf("logged %d records to context logger, want 1", len(records))
	}
	for key, want := range map[string]any{
		"level": "DEBUG", "msg": "command finished", "component": "exec",
		"command": "true", "exit_code": float64(0), "stderr": "",
	} {
		if got := records[0][key]; got != want {
			t.Errorf("log %s = %v, want %v", key, got, want)
		}
	}
}

// --- the output cap ----------------------------------------------------

// A command that produces more than MaxOutput is allowed to finish; only
// what is kept is bounded. The alternative -- failing the write -- would
// kill a command for being chatty.
func TestExecCommandRunner_Run_CapsOutput(t *testing.T) {
	runner := NewExecCommandRunner()

	result, err := runner.Run(context.Background(), Command{
		Name:      "sh",
		Args:      []string{"-c", "printf 'y%.0s' $(seq 1 5000); printf 'e%.0s' $(seq 1 5000) >&2"},
		Timeout:   10 * time.Second,
		MaxOutput: 1000,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Stdout) != 1000 {
		t.Errorf("kept %d bytes of stdout, want 1000", len(result.Stdout))
	}
	if len(result.Stderr) != 1000 {
		t.Errorf("kept %d bytes of stderr, want 1000", len(result.Stderr))
	}
	// Said, not inferred: output ending exactly at the cap is otherwise
	// indistinguishable from output that happened to end there.
	if !result.StdoutTruncated || !result.StderrTruncated {
		t.Errorf("truncation not reported: %+v", result)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d — the command should have been allowed to finish",
			result.ExitCode)
	}
}

// Zero is what every command Jail Deck composes itself uses: their output is
// bounded by what they are, and a cap there would be a limit nobody chose.
func TestExecCommandRunner_Run_KeepsEverythingByDefault(t *testing.T) {
	runner := NewExecCommandRunner()

	result, err := runner.Run(context.Background(), Command{
		Name: "sh", Args: []string{"-c", "printf 'y%.0s' $(seq 1 5000)"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Stdout) != 5000 {
		t.Errorf("kept %d bytes, want all 5000", len(result.Stdout))
	}
	if result.StdoutTruncated {
		t.Error("reported truncation with no cap set")
	}
}

// Exactly at the cap is not truncation: nothing was dropped.
func TestExecCommandRunner_Run_OutputExactlyAtTheCap(t *testing.T) {
	runner := NewExecCommandRunner()

	result, err := runner.Run(context.Background(), Command{
		Name: "sh", Args: []string{"-c", "printf 'yyyy'"},
		Timeout: 10 * time.Second, MaxOutput: 4,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.StdoutTruncated {
		t.Error("output that fit exactly was reported as truncated")
	}
}

// A command that is not on the PATH never ran. It is marked as that, and the
// message names the PATH it looked on: rc gives services
// PATH=/sbin:/bin:/usr/sbin:/usr/bin, and "smartctl: not found" sent nobody to
// /usr/local/sbin, where it was all along (#95).
func TestExecCommandRunner_Run_MissingCommandIsMarkedAndNamesThePath(t *testing.T) {
	t.Setenv("PATH", "/nonexistent-for-this-test")
	result, err := NewExecCommandRunner().Run(context.Background(),
		Command{Name: "jaildeck-no-such-command"})
	if !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("err = %v, want ErrCommandNotFound", err)
	}
	if !strings.Contains(err.Error(), "/nonexistent-for-this-test") {
		t.Errorf("err = %q; it should name the PATH it searched", err)
	}
	if result.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1 for a command that never ran", result.ExitCode)
	}
}

// A command that ran and failed is not "not found", whatever it printed.
func TestExecCommandRunner_Run_AFailingCommandIsNotNotFound(t *testing.T) {
	_, err := NewExecCommandRunner().Run(context.Background(),
		Command{Name: "sh", Args: []string{"-c", "echo 'not found' >&2; exit 127"}})
	if err == nil || errors.Is(err, ErrCommandNotFound) {
		t.Errorf("err = %v; a command that ran is never ErrCommandNotFound", err)
	}
}

// backgrounded runs script, which must print the pid of a child it leaves
// running the way vm start leaves vm-run -- in the background, its output on
// /dev/null -- and returns that pid. The child is killed when the test ends.
func backgrounded(t *testing.T, cmd Command) (int, error) {
	t.Helper()
	result, err := (&ExecCommandRunner{}).Run(context.Background(), cmd)
	pid, convErr := strconv.Atoi(strings.TrimSpace(strings.SplitN(result.Stdout, "\n", 2)[0]))
	if convErr != nil {
		t.Fatalf("no pid in %q (err %v)", result.Stdout, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid, err
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// gone waits briefly for pid to be reaped: a killed child is a zombie until
// whatever adopted it collects it.
func gone(pid int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestExecCommandRunner_Run_DetachedLeavesItsChildOutsideJailDecksSession(t *testing.T) {
	start := time.Now()
	pid, err := backgrounded(t, Command{
		Name: "sh", Args: []string{"-c", "sleep 30 >/dev/null 2>&1 & echo $!"}, Detach: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The child's output is /dev/null, so nothing holds the pipes.
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Run blocked for %s", time.Since(start))
	}
	if !alive(pid) {
		t.Fatal("the backgrounded child did not survive the command")
	}
	// A session, not only a group: Setpgid alone gives a group of its own
	// too, inside Jail Deck's session.
	if getsid(t, pid) == getsid(t, 0) {
		t.Errorf("the child is in Jail Deck's session %d", getsid(t, 0))
	}
}

func getsid(t *testing.T, pid int) int {
	t.Helper()
	sid, _, errno := syscall.RawSyscall(syscall.SYS_GETSID, uintptr(pid), 0, 0)
	if errno != 0 {
		t.Fatalf("getsid(%d): %v", pid, errno)
	}
	return int(sid)
}

// What Detach is for: a timeout must not take down what the command left
// running -- for vm start, the guest. Without it, it does.
func TestExecCommandRunner_Run_ATimeoutSignalsOnlyTheDetachedCommand(t *testing.T) {
	script := "sleep 30 >/dev/null 2>&1 & echo $!; exec sleep 30"

	pid, err := backgrounded(t, Command{
		Name: "sh", Args: []string{"-c", script}, Timeout: 200 * time.Millisecond, Detach: true,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a timeout, got %v", err)
	}
	if gone(pid) {
		t.Error("a detached command's timeout killed the child it left running")
	}

	pid, err = backgrounded(t, Command{
		Name: "sh", Args: []string{"-c", script}, Timeout: 200 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a timeout, got %v", err)
	}
	if !gone(pid) {
		t.Error("a plain command's timeout left its child running")
	}
}
