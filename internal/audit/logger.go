package audit

import (
	"context"
	"time"
)

type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	Operation string    `json:"operation"`
	Target    string    `json:"target"`
	Command   string    `json:"command"`
	ExitCode  int       `json:"exit_code"`
	Success   bool      `json:"success"`
	TimedOut  bool      `json:"timed_out,omitempty"`
	Error     string    `json:"error,omitempty"`
	// RequestID is the caller's X-Request-Id, omitted when it sent none
	// (JD-011: nothing is stored that the request did not supply).
	RequestID string `json:"request_id,omitempty"`

	// Who opened a console session ("session", or "token:<name>"), how long
	// it lasted, and why it ended (JD-042). DurationMS is also an exec's, how
	// long the command ran (#141). Omitted on every other entry.
	Principal  string `json:"principal,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type Logger interface {
	Log(ctx context.Context, entry Entry) error
}

type Reader interface {
	Recent(ctx context.Context, limit int, filter Filter) ([]Entry, error)
}
