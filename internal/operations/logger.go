package operations

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
	Error     string    `json:"error,omitempty"`
}

type Filter struct {
	Operation string
	Targets   []string
	Success   *bool
}

type Logger interface {
	Log(ctx context.Context, entry Entry) error
}

type Reader interface {
	Recent(ctx context.Context, limit int, filter Filter) ([]Entry, error)
}
