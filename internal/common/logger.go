package common

import (
	"context"
	"log/slog"
)

type loggerKey struct{}

// NewComponentLogger returns a logger tagged with the component name, so every
// record says which part of Jail Deck produced it.
//
// Call it at the point of use. Never assign the result to a package-level var:
// slog.Default() is replaced in main after package initialisation, so a var
// captures the pre-configuration logger — which routes through the standard log
// package. Records then arrive at the wrong level with their attributes
// flattened into the message string, which still looks plausible in the output.
func NewComponentLogger(component string) *slog.Logger {
	return slog.Default().With("component", component)
}

// Logger composes the request-scoped logger with a component tag. Prefer it
// wherever a ctx is available; NewComponentLogger is for the call sites that
// have none.
func Logger(ctx context.Context, component string) *slog.Logger {
	return LoggerFromContext(ctx).With("component", component)
}

// ContextWithLogger returns a copy of ctx carrying l. The returned context must
// be used — contexts are immutable, so discarding it is a silent no-op and
// LoggerFromContext will quietly fall back to the default.
func ContextWithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// LoggerFromContext returns the request-scoped logger, or the default logger
// when ctx carries none. It never returns nil, so callers outside a request —
// CLI paths, tests — need no special case.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
