package audit

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

type Filter struct {
	Operation string
	Targets   []string
	// RequestID matches exactly and case-sensitively: it is the client's own
	// opaque id, not a name Jail Deck normalises.
	RequestID string
	Success   *bool
	TimedOut  *bool
	// Before pages backwards: entries strictly older than this, newest first.
	// A cursor rather than an offset because the audit log is an append-only
	// file that newsyslog rotates and new entries extend — both of which shift
	// what an offset means between one request and the next.
	Before *time.Time
}

func parseFilter(q url.Values) (Filter, error) {
	var f Filter

	f.Operation = strings.ToLower(q.Get("operation"))

	// An unparseable request id matches nothing rather than erroring: it is a
	// filter, and "no entry carries that id" is the honest answer to a value no
	// entry could carry.
	f.RequestID = q.Get("request_id")

	f.Success = parseBool(q.Get("success"))
	f.TimedOut = parseBool(q.Get("timed_out"))

	for part := range strings.SplitSeq(q.Get("targets"), ",") {
		if name := strings.TrimSpace(part); name != "" {
			f.Targets = append(f.Targets, name)
		}
	}

	// An unreadable limit falls back to the default, because a wrong window is
	// harmless. An unreadable cursor does not: ignoring it would silently
	// return the first page again, so a client paging backwards would loop on
	// the same rows rather than be told it asked for something impossible.
	if raw := q.Get("before"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			// Go's parse error names the layout string, which is an
			// implementation detail; the client needs the format, not ours.
			return Filter{}, errors.New("before must be an RFC 3339 timestamp, such as the timestamp of an entry already returned")
		}
		f.Before = &t
	}

	return f, nil
}

func parseBool(v string) *bool {
	switch v {
	case "true":
		t := true
		return &t

	case "false":
		t := false
		return &t
	}
	return nil
}
