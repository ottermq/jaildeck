package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
