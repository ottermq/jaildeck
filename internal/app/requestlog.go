package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// slogFormatter builds one entry per request. chi calls NewLogEntry before the
// handler runs and entry.Write after it returns, so the entry is where
// per-request state lives in between.
type slogFormatter struct{}

func (slogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	return &requestLogEntry{
		logger: slog.Default().With("method", r.Method, "path", r.URL.Path),
	}
}

// compile-time check
var _ middleware.LogEntry = (*requestLogEntry)(nil)

type requestLogEntry struct{ logger *slog.Logger }

// AddAttrs lets middleware running after RequestLogger enrich the record before
// it is written. RequireAuth adds the principal this way.
func (e *requestLogEntry) AddAttrs(args ...any) {
	e.logger = e.logger.With(args...)
}

func (e *requestLogEntry) Write(status, bytes int, _ http.Header, elapsed time.Duration, _ any) {
	e.logger.Log(context.Background(), levelForStatus(status), "request",
		"status", status,
		"bytes", bytes,
		"duration", elapsed)
}

func (e *requestLogEntry) Panic(v any, stack []byte) {
	e.logger.Error("request panicked", "panic", v, "stack", string(stack))
}

func levelForStatus(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
