package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLevelForStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   slog.Level
	}{
		{name: "below warning", status: 399, want: slog.LevelInfo},
		{name: "first warning", status: 400, want: slog.LevelWarn},
		{name: "last warning", status: 499, want: slog.LevelWarn},
		{name: "first error", status: 500, want: slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := levelForStatus(tt.status); got != tt.want {
				t.Errorf("levelForStatus(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func captureRequestLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

func assertRequestLog(t *testing.T, logs *bytes.Buffer, want map[string]any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		t.Fatalf("decode request log: %v; logs: %s", err, logs.String())
	}
	if decoder.More() {
		t.Fatalf("expected exactly one log record; logs: %s", logs.String())
	}
	for key, value := range want {
		if got := record[key]; got != value {
			t.Errorf("log %s = %v, want %v", key, got, value)
		}
	}
}

func TestRoutesLogRequestOnceThroughSlog(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	logs := captureRequestLogs(t)
	rec := httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/jails", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertRequestLog(t, logs, map[string]any{
		"msg": "request", "level": "WARN", "method": "GET",
		"path": "/api/jails", "status": float64(http.StatusUnauthorized),
	})
}

func TestRequestLogEntryWrite(t *testing.T) {
	for _, tc := range []struct {
		status int
		level  string
	}{
		{http.StatusOK, "INFO"},
		{http.StatusUnauthorized, "WARN"},
		{http.StatusInternalServerError, "ERROR"},
	} {
		t.Run(tc.level, func(t *testing.T) {
			logs := captureRequestLogs(t)
			entry := (slogFormatter{}).NewLogEntry(httptest.NewRequest(http.MethodPost, "/jails?limit=1", nil)).(*requestLogEntry)
			entry.AddAttrs("principal", "operator")
			entry.Write(tc.status, 42, nil, time.Second, nil)
			assertRequestLog(t, logs, map[string]any{
				"msg": "request", "level": tc.level, "method": "POST",
				"path": "/jails", "status": float64(tc.status),
				"principal": "operator", "bytes": float64(42), "duration": float64(time.Second),
			})
		})
	}
}

func TestRequestLogEntryPanic(t *testing.T) {
	logs := captureRequestLogs(t)
	entry := (slogFormatter{}).NewLogEntry(httptest.NewRequest(http.MethodGet, "/jails", nil))
	entry.Panic("boom", []byte("test stack"))
	assertRequestLog(t, logs, map[string]any{
		"msg": "request panicked", "level": "ERROR", "method": "GET",
		"path": "/jails", "panic": "boom", "stack": "test stack",
	})
}
