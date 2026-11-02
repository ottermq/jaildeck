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

	// Who the request that wrote the entry authenticated as: "session", or
	// "token:<name>". Every entry a request writes has one, a detached
	// operation's too; only what no request started, such as the observer's,
	// has none.
	Principal string `json:"principal,omitempty"`
	// How long a console session lasted and why it ended (JD-042).
	// DurationMS is also an exec's, how long the command ran (#141). Omitted
	// on every other entry.
	DurationMS int64  `json:"duration_ms,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// What the client said about a console it opened (#155): on whose
	// behalf, and why. Verbatim, never required; a platform with tenants
	// matches them to its own record. On console-open entries only.
	Actor      string `json:"actor,omitempty"`
	OpenReason string `json:"open_reason,omitempty"`
}

type Logger interface {
	Log(ctx context.Context, entry Entry) error
}

type Reader interface {
	Recent(ctx context.Context, limit int, filter Filter) ([]Entry, error)
}
