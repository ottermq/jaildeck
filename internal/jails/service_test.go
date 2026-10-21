package jails

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

// Test doubles live here rather than coming from internal/system/fake: that
// package imports this one, so depending on it would be an import cycle.

type fakeLogger struct{ entries []audit.Entry }

func (f *fakeLogger) Log(_ context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return nil
}

type stubSystem struct {
	command string
	err     error
}

func (s stubSystem) List(context.Context) ([]Jail, error) { return nil, s.err }
func (s stubSystem) Start(context.Context, string) (OperationResult, error) {
	return OperationResult{Command: s.command}, s.err
}
func (s stubSystem) Stop(context.Context, string) (OperationResult, error) {
	return OperationResult{Command: s.command}, s.err
}
func (s stubSystem) Restart(context.Context, string) (OperationResult, error) {
	return OperationResult{Command: s.command}, s.err
}

// Checked when this file compiles, so a mistyped or renamed method fails here
// instead of at the first use. fakeLogger needs the pointer form because Log
// has a pointer receiver.
var (
	_ JailSystem   = stubSystem{}
	_ audit.Logger = (*fakeLogger)(nil)
	_ audit.Logger = erroringLogger{}
)

// erroringLogger stands in for an audit log that cannot be written — a full
// disk, a revoked permission, a removed mount.
type erroringLogger struct{}

func (erroringLogger) Log(context.Context, audit.Entry) error {
	return errors.New("no space left on device")
}

// captureLogs redirects the default slog logger into a buffer for the duration
// of one test. It swaps a global, so no test using it may call t.Parallel.
//
// The handler emits JSON so tests can assert on exact field values. Matching
// substrings of text output is unreliable: "component=jail" is also a substring
// of "component=jails".
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return &buf
}

// logRecord is one decoded line of captureLogs output.
type logRecord struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	Component string `json:"component"`
	Operation string `json:"operation"`
	Target    string `json:"target"`
	Err       string `json:"err"`
	Method    string `json:"method"`
	Path      string `json:"path"`
}

// decodeLogs returns every record written to buf, in order.
func decodeLogs(t *testing.T, buf *bytes.Buffer) []logRecord {
	t.Helper()

	var records []logRecord
	decoder := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for decoder.More() {
		var rec logRecord
		if err := decoder.Decode(&rec); err != nil {
			t.Fatalf("decoding log output: %v\nraw: %s", err, buf.String())
		}
		records = append(records, rec)
	}
	return records
}

func TestToAppError(t *testing.T) {
	timedOut := &system.CommandError{
		Command: "service",
		Err:     fmt.Errorf("service timed out after 2m30s: %w", context.DeadlineExceeded),
	}
	cancelled := &system.CommandError{
		Command: "service",
		Err:     fmt.Errorf("service cancelled: %w", context.Canceled),
	}

	tests := []struct {
		name     string
		err      error
		wantCode int
		wantSame error // non-nil when the error must come back unchanged
	}{
		{"timeout becomes 504", timedOut, http.StatusGatewayTimeout, nil},
		{"cancellation is not a timeout", cancelled, http.StatusInternalServerError, nil},
		{"existing AppError passes through", ErrJailNotFound, http.StatusNotFound, ErrJailNotFound},
		{"anything else becomes 500", errors.New("boom"), http.StatusInternalServerError, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := toAppError("ottermq", OperationStop, tt.err)

			var appErr *common.AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("toAppError() = %T, want *common.AppError", err)
			}
			if appErr.Code != tt.wantCode {
				t.Errorf("Code = %d, want %d", appErr.Code, tt.wantCode)
			}
			if tt.wantSame != nil && !errors.Is(err, tt.wantSame) {
				t.Error("error should have been returned unchanged")
			}
		})
	}

	if toAppError("ottermq", OperationStop, nil) != nil {
		t.Error("nil error should map to nil")
	}
}

func TestLogJailOperation_MarksTimeouts(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		wantCommand  string
		err          error
		wantTimedOut bool
	}{
		{
			"timeout is flagged",
			"service jail stop ottermq",
			"service jail stop ottermq",
			&system.CommandError{Err: fmt.Errorf("x: %w", context.DeadlineExceeded)},
			true,
		},
		{
			"ordinary failure is not",
			"service jail stop ottermq",
			"service jail stop ottermq",
			&system.CommandError{Err: errors.New("boom")},
			false,
		},
		{
			"nothing ran is recorded as nothing",
			"",
			"",
			&system.CommandError{Err: errors.New("boom")},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := &fakeLogger{}
			svc := NewJailService(stubSystem{command: tt.command, err: tt.err}, logger)

			if _, err := svc.Stop(context.Background(), JailName("ottermq")); err == nil {
				t.Fatal("Stop() returned nil error, want a failure")
			}
			if len(logger.entries) != 1 {
				t.Fatalf("logged %d entries, want 1", len(logger.entries))
			}

			got := logger.entries[0]
			if got.TimedOut != tt.wantTimedOut {
				t.Errorf("TimedOut = %v, want %v", got.TimedOut, tt.wantTimedOut)
			}
			if got.Success {
				t.Error("Success = true, want false")
			}
			if got.Operation != "stop" {
				t.Errorf("Operation = %q, want %q", got.Operation, "stop")
			}

			if got.Command != tt.wantCommand {
				t.Errorf("Command = %q, want %q", got.Command, tt.wantCommand)
			}
		})
	}
}

func TestLogJailOperation_HappyPath(t *testing.T) {
	logger := &fakeLogger{}
	svc := NewJailService(stubSystem{command: "service jail stop ottermq"}, logger)

	if _, err := svc.Stop(context.Background(), JailName("ottermq")); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if len(logger.entries) != 1 {
		t.Fatalf("logged %d entries, want 1", len(logger.entries))
	}

	got := logger.entries[0]
	if !got.Success {
		t.Error("Success = false, want true")
	}
	if got.TimedOut {
		t.Error("TimedOut = true, want false")
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want empty", got.Error)
	}
	if got.Operation != "stop" {
		t.Errorf("Operation = %q, want %q", got.Operation, "stop")
	}
	if got.Command != "service jail stop ottermq" {
		t.Errorf("Command = %q, want %q", got.Command, "service jail stop ottermq")
	}
}

func TestLogJailOperation_AuditFailureIsLoggedAtError(t *testing.T) {
	ctx, logs := captureRequestLogs(t, "component", "jail", "method", "POST", "path", "/jails/ottermq/start")
	svc := NewJailService(stubSystem{command: "service jail start ottermq"}, erroringLogger{})

	// An audit write that fails must not fail the operation: the jail has
	// already started, so reporting an error would be a lie (JD-011, #31).
	if _, err := svc.Start(ctx, JailName("ottermq")); err != nil {
		t.Fatalf("Start() error = %v, want nil — an audit failure must not fail the operation", err)
	}

	records := decodeLogs(t, logs)
	if len(records) != 1 {
		t.Fatalf("logged %d records, want 1: %s", len(records), logs.String())
	}

	got := records[0]
	want := logRecord{
		Level:     "ERROR",
		Msg:       "failed to write operation log",
		Component: "jail",
		Operation: "start",
		Target:    "ottermq",
		Err:       "no space left on device",
		Method:    "POST",
		Path:      "/jails/ottermq/start",
	}
	if got != want {
		t.Errorf("log record = %+v, want %+v", got, want)
	}
}

// captureRequestLogs points the default logger at a buffer this test never reads
// and returns one fed only by a request-scoped logger in the returned context.
// A record that falls back to the default is therefore absent here — which is
// what makes common.Logger(ctx, …) and common.NewComponentLogger(…)
// distinguishable. Sharing one buffer makes any such assertion vacuous.
func captureRequestLogs(t *testing.T, attrs ...any) (context.Context, *bytes.Buffer) {
	t.Helper()
	captureLogs(t) // redirect the default away from this test's assertions
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})).With(attrs...)
	return common.ContextWithLogger(context.Background(), l), &buf
}
