package audit

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestValidateRequestID(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "empty is not an error", raw: "", want: ""},
		{name: "hyphenated uuid", raw: "3f2504e0-4f89-11d3-9a0c-0305e82c3301", want: "3f2504e0-4f89-11d3-9a0c-0305e82c3301"},
		{name: "ulid", raw: "01ARZ3NDEKTSV4RRFFQ69G5FAV", want: "01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		{name: "trace and span", raw: "trace:4bf92f-00f067aa0ba902b7", want: "trace:4bf92f-00f067aa0ba902b7"},
		{name: "dotted", raw: "otter.job.1821", want: "otter.job.1821"},
		{name: "underscored", raw: "otter_job_1821", want: "otter_job_1821"},
		{name: "at the length limit", raw: strings.Repeat("a", MaxRequestIDLen), want: strings.Repeat("a", MaxRequestIDLen)},
		{name: "one over the limit", raw: strings.Repeat("a", MaxRequestIDLen+1), wantErr: true},
		{name: "a space", raw: "otter job", wantErr: true},
		{name: "a newline, which would forge an audit line", raw: "a\nb", wantErr: true},
		{name: "a carriage return", raw: "a\rb", wantErr: true},
		{name: "a quote, which would break the JSON", raw: `a"b`, wantErr: true},
		{name: "a backslash", raw: `a\b`, wantErr: true},
		{name: "a slash", raw: "a/b", wantErr: true},
		{name: "non-ascii", raw: "jöb", wantErr: true},
		{name: "a NUL", raw: "a\x00b", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateRequestID(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateRequestID(%q) = %q, nil; want an error", tt.raw, got)
				}
				if got != "" {
					t.Errorf("ValidateRequestID(%q) returned %q alongside its error; want the id dropped", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateRequestID(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("ValidateRequestID(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// The error names the header and says what was wrong, because it is what the
// operator reads in the log when a client's correlation ids stop appearing.
func TestValidateRequestIDErrorIsDiagnostic(t *testing.T) {
	for _, raw := range []string{"otter job", strings.Repeat("a", MaxRequestIDLen+1)} {
		_, err := ValidateRequestID(raw)
		if err == nil {
			t.Fatalf("ValidateRequestID(%q) returned no error", raw)
		}
		if !strings.Contains(err.Error(), RequestIDHeader) {
			t.Errorf("error %q does not name %s", err, RequestIDHeader)
		}
	}
}

func TestRequestIDContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := RequestIDFromContext(ctx); got != "" {
		t.Errorf("RequestIDFromContext(empty) = %q, want \"\"", got)
	}
	ctx = ContextWithRequestID(ctx, "otter.job.1")
	if got := RequestIDFromContext(ctx); got != "otter.job.1" {
		t.Errorf("RequestIDFromContext() = %q, want otter.job.1", got)
	}
}

// The stamping lives in FileLogger.Log so that no call site has to remember it.
func TestFileLoggerStampsRequestIDFromContext(t *testing.T) {
	logger := NewFileLogger(t.TempDir() + "/audit.log")
	ctx := ContextWithRequestID(context.Background(), "otter.job.7")

	if err := logger.Log(ctx, Entry{Operation: "start", Target: "nginx", Success: true}); err != nil {
		t.Fatalf("Log() error = %v", err)
	}
	// An entry that already carries one keeps it, so a caller can set it
	// explicitly where there is no request context at all.
	if err := logger.Log(ctx, Entry{Operation: "stop", Target: "nginx", RequestID: "explicit"}); err != nil {
		t.Fatalf("Log() error = %v", err)
	}
	// And a context without one leaves the field empty rather than inventing it.
	if err := logger.Log(context.Background(), Entry{Operation: "restart", Target: "nginx"}); err != nil {
		t.Fatalf("Log() error = %v", err)
	}

	entries, err := logger.Recent(context.Background(), 10, Filter{})
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Operation] = e.RequestID
	}
	want := map[string]string{"start": "otter.job.7", "stop": "explicit", "restart": ""}
	for op, id := range want {
		if got[op] != id {
			t.Errorf("%s entry request_id = %q, want %q", op, got[op], id)
		}
	}
}

func TestRecentFiltersByRequestID(t *testing.T) {
	logger := NewFileLogger(t.TempDir() + "/audit.log")
	for _, id := range []string{"job-a", "job-b", "job-a"} {
		ctx := ContextWithRequestID(context.Background(), id)
		if err := logger.Log(ctx, Entry{Operation: "start", Target: "nginx"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := logger.Log(context.Background(), Entry{Operation: "start", Target: "nginx"}); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		filter string
		want   int
	}{
		{"job-a", 2},
		{"job-b", 1},
		{"JOB-A", 0}, // exact and case-sensitive: it is the client's opaque id
		{"nothing-sent-this", 0},
		{"", 4}, // no filter
	} {
		entries, err := logger.Recent(context.Background(), 10, Filter{RequestID: tt.filter})
		if err != nil {
			t.Fatalf("Recent(%q) error = %v", tt.filter, err)
		}
		if len(entries) != tt.want {
			t.Errorf("Recent(request_id=%q) returned %d entries, want %d", tt.filter, len(entries), tt.want)
		}
	}
}

// An entry with no request id must not grow a "request_id":"" field, so a
// client that never sends the header sees the shape it saw before (JD-011).
func TestEntryOmitsEmptyRequestID(t *testing.T) {
	logger := NewFileLogger(t.TempDir() + "/audit.log")
	if err := logger.Log(context.Background(), Entry{Operation: "start", Target: "nginx"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(logger.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "request_id") {
		t.Errorf("audit line = %s, want no request_id field at all", raw)
	}
}
