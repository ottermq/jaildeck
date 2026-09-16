package jails

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"codeberg.org/Otter-Labs/jaildeck/internal/audit"
	"codeberg.org/Otter-Labs/jaildeck/internal/common"
	"codeberg.org/Otter-Labs/jaildeck/internal/system"
)

// Test doubles live here rather than coming from internal/system/fake: that
// package imports this one, so depending on it would be an import cycle.

type fakeLogger struct{ entries []audit.Entry }

func (f *fakeLogger) Log(_ context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return nil
}

type stubSystem struct{ err error }

func (s stubSystem) List(context.Context) ([]Jail, error)          { return nil, s.err }
func (s stubSystem) Start(context.Context, string) (Jail, error)   { return Jail{}, s.err }
func (s stubSystem) Stop(context.Context, string) (Jail, error)    { return Jail{}, s.err }
func (s stubSystem) Restart(context.Context, string) (Jail, error) { return Jail{}, s.err }

// Checked when this file compiles, so a mistyped or renamed method fails here
// instead of at the first use. fakeLogger needs the pointer form because Log
// has a pointer receiver.
var (
	_ JailSystem   = stubSystem{}
	_ audit.Logger = (*fakeLogger)(nil)
)

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
		err          error
		wantTimedOut bool
	}{
		{"timeout is flagged", &system.CommandError{Err: fmt.Errorf("x: %w", context.DeadlineExceeded)}, true},
		{"ordinary failure is not", &system.CommandError{Err: errors.New("boom")}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := &fakeLogger{}
			svc := NewJailService(stubSystem{err: tt.err}, logger)

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
		})
	}
}

func TestLogJailOperation_HappyPath(t *testing.T) {
	logger := &fakeLogger{}
	svc := NewJailService(stubSystem{}, logger)

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
}
