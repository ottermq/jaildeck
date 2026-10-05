package common

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestContextWithLoggerRoundTrip(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil)).With("request_id", "request-123")
	ctx := ContextWithLogger(context.Background(), logger)

	got := LoggerFromContext(ctx)
	if got != logger {
		t.Fatal("LoggerFromContext did not return the stored logger")
	}
	got.Info("round-trip")
	assertLogAttribute(t, &output, "request_id", "request-123")
}

func TestLoggerFromContextFallback(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	logger := LoggerFromContext(context.Background())
	if logger == nil {
		t.Fatal("LoggerFromContext returned nil")
	}
	if logger != slog.Default() {
		t.Fatal("LoggerFromContext did not return the default logger")
	}
	logger.Info("fallback")
	assertLogAttribute(t, &output, "msg", "fallback")
}

func TestLoggerComposesRequestAndComponent(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil)).With("request_id", "request-456")
	ctx := ContextWithLogger(context.Background(), logger)

	Logger(ctx, "jails").Info("composed")
	assertLogAttribute(t, &output, "request_id", "request-456")
	assertLogAttribute(t, &output, "component", "jails")
}

func TestContextWithLoggerLeavesParentUnchanged(t *testing.T) {
	parent := context.Background()
	original := LoggerFromContext(parent)
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	ContextWithLogger(parent, logger)

	if got := LoggerFromContext(parent); got != original {
		t.Fatal("ContextWithLogger changed the parent context")
	}
}

func assertLogAttribute(t *testing.T, output *bytes.Buffer, key, want string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode log record: %v", err)
	}
	if got := record[key]; got != want {
		t.Errorf("log attribute %q = %v, want %q", key, got, want)
	}
}
