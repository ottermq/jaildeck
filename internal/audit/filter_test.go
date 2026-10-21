package audit

import (
	"net/url"
	"slices"
	"testing"
	"time"
)

func TestApplyFilters(t *testing.T) {
	tests := []struct {
		name   string
		entry  Entry
		filter Filter
		want   bool
	}{
		{
			name:  "no filters accepts any entry",
			entry: Entry{Operation: "start", Target: "ottermq", Success: true, TimedOut: true},
			want:  true,
		},
		{
			name:   "operation matches",
			entry:  Entry{Operation: "start"},
			filter: Filter{Operation: "start"},
			want:   true,
		},
		{
			name:   "operation differs",
			entry:  Entry{Operation: "stop"},
			filter: Filter{Operation: "start"},
		},
		{
			name:   "operation matching is case sensitive",
			entry:  Entry{Operation: "START"},
			filter: Filter{Operation: "start"},
		},
		{
			name:   "success true matches",
			entry:  Entry{Success: true},
			filter: Filter{Success: boolPtr(true)},
			want:   true,
		},
		{
			name:   "success true rejects failure",
			filter: Filter{Success: boolPtr(true)},
		},
		{
			name:   "success false matches",
			filter: Filter{Success: boolPtr(false)},
			want:   true,
		},
		{
			name:   "success false rejects success",
			entry:  Entry{Success: true},
			filter: Filter{Success: boolPtr(false)},
		},
		{
			name:   "single target matches",
			entry:  Entry{Target: "ottermq"},
			filter: Filter{Targets: []string{"ottermq"}},
			want:   true,
		},
		{
			name:   "any listed target matches",
			entry:  Entry{Target: "goodiesdb"},
			filter: Filter{Targets: []string{"ottermq", "goodiesdb"}},
			want:   true,
		},
		{
			name:   "unlisted target is rejected",
			entry:  Entry{Target: "other"},
			filter: Filter{Targets: []string{"ottermq", "goodiesdb"}},
		},
		{
			name:   "empty non-nil targets rejects entry",
			filter: Filter{Targets: []string{}},
		},
		{
			name:   "timed out true matches",
			entry:  Entry{TimedOut: true},
			filter: Filter{TimedOut: boolPtr(true)},
			want:   true,
		},
		{
			name:   "timed out true rejects non-timeout",
			filter: Filter{TimedOut: boolPtr(true)},
		},
		{
			name:   "timed out false matches",
			filter: Filter{TimedOut: boolPtr(false)},
			want:   true,
		},
		{
			name:   "timed out false rejects timeout",
			entry:  Entry{TimedOut: true},
			filter: Filter{TimedOut: boolPtr(false)},
		},
		{
			name:   "all filters match",
			entry:  Entry{Operation: "start", Target: "ottermq", Success: true},
			filter: Filter{Operation: "start", Targets: []string{"ottermq"}, Success: boolPtr(true), TimedOut: boolPtr(false)},
			want:   true,
		},
		{
			name:   "one mismatching filter rejects entry",
			entry:  Entry{Operation: "start", Target: "ottermq", Success: true, TimedOut: true},
			filter: Filter{Operation: "start", Targets: []string{"ottermq"}, Success: boolPtr(true), TimedOut: boolPtr(false)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := applyFilters(tt.entry, tt.filter); got != tt.want {
				t.Errorf("applyFilters(%+v, %+v) = %v, want %v", tt.entry, tt.filter, got, tt.want)
			}
		})
	}
}

func TestParseFilter(t *testing.T) {
	tests := []struct {
		name          string
		query         url.Values
		wantOperation string
		wantSuccess   *bool
		wantTimedOut  *bool
		wantTargets   []string
	}{
		{
			name:  "empty filter map",
			query: url.Values{},
		},
		{
			name:          "operation is lowercased",
			query:         url.Values{"operation": {"START"}},
			wantOperation: "start",
		},
		{
			name:        "single target",
			query:       url.Values{"targets": {"ottermq"}},
			wantTargets: []string{"ottermq"},
		},
		{
			name:        "multiple targets with spacing",
			query:       url.Values{"targets": {"ottermq, goodiesdb"}},
			wantTargets: []string{"ottermq", "goodiesdb"},
		},
		{
			name:        "embedded empty elements are dropped",
			query:       url.Values{"targets": {"ottermq,,goodiesdb,"}},
			wantTargets: []string{"ottermq", "goodiesdb"},
		},
		{
			// Regression test for #69: a whitespace-only value must be treated
			// as "no filter", not as a filter that matches nothing.
			name:  "whitespace-only targets is treated as no filter",
			query: url.Values{"targets": {" "}},
		},
		{
			// Regression test for #69: a lone comma used to panic.
			name:  "comma-only targets is treated as no filter",
			query: url.Values{"targets": {","}},
		},
		{
			// parseBool's own rules are TestParseBool's job; this row only has
			// to prove each parameter lands on its own field. Distinct values
			// so a swapped assignment fails.
			name:         "success and timed_out are independent",
			query:        url.Values{"success": {"true"}, "timed_out": {"false"}},
			wantSuccess:  boolPtr(true),
			wantTimedOut: boolPtr(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got, err := parseFilter(tt.query)
			if err != nil {
				t.Fatalf("parseFilter() error = %v", err)
			}

			if got.Operation != tt.wantOperation {
				t.Errorf("Operation = %q, want %q", got.Operation, tt.wantOperation)
			}
			if tt.wantTargets == nil {
				if got.Targets != nil {
					t.Errorf("Targets = %#v, want nil (no filter)", got.Targets)
				}
			} else if !slices.Equal(got.Targets, tt.wantTargets) {
				t.Errorf("Targets = %#v, want %#v", got.Targets, tt.wantTargets)
			}
			if (got.Success == nil) != (tt.wantSuccess == nil) {
				t.Fatalf("Success = %v, want %v", got.Success, tt.wantSuccess)
			}
			if got.Success != nil && *got.Success != *tt.wantSuccess {
				t.Errorf("Success = %v, want %v", *got.Success, *tt.wantSuccess)
			}
			if (got.TimedOut == nil) != (tt.wantTimedOut == nil) {
				t.Fatalf("TimedOut = %v, want %v", got.TimedOut, tt.wantTimedOut)
			}
			if got.TimedOut != nil && *got.TimedOut != *tt.wantTimedOut {
				t.Errorf("TimedOut = %v, want %v", *got.TimedOut, *tt.wantTimedOut)
			}
		})
	}
}

func TestParseBool(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *bool
	}{
		{
			name:     "input is 'true'",
			input:    "true",
			expected: boolPtr(true),
		},
		{
			name:     "input is 'false'",
			input:    "false",
			expected: boolPtr(false),
		},
		{
			name:     "input is empty",
			input:    "",
			expected: nil,
		},
		{
			name:     "input is 'bogus'",
			input:    "bogus",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBool(tt.input)
			if tt.expected == nil {
				if got != nil {
					t.Fatalf("parseBool(%q) = %v, want nil", tt.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseBool(%q) = nil, want %v", tt.input, *tt.expected)
			}
			if *got != *tt.expected {
				t.Fatalf("parseBool(%q) = %v, want %v", tt.input, *got, *tt.expected)
			}
		})
	}
}

func TestParseFilterCursor(t *testing.T) {
	for _, tt := range []struct {
		name    string
		raw     string
		wantErr bool
		want    time.Time
	}{
		{name: "absent leaves the cursor unset", raw: ""},
		{
			name: "RFC 3339 with nanoseconds, as the API emits",
			raw:  "2026-10-20T23:57:44.123456789-03:00",
			want: time.Date(2026, 10, 20, 23, 57, 44, 123456789, time.FixedZone("", -3*60*60)),
		},
		{name: "RFC 3339 without fractional seconds", raw: "2026-10-20T23:57:44Z",
			want: time.Date(2026, 10, 20, 23, 57, 44, 0, time.UTC)},
		{name: "not a timestamp", raw: "yesterday", wantErr: true},
		{name: "a bare date is not enough", raw: "2026-10-20", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFilter(url.Values{"before": {tt.raw}})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseFilter(%q) error = nil, want an error — a cursor that cannot be read must not be ignored", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFilter(%q) error = %v", tt.raw, err)
			}
			if tt.raw == "" {
				if got.Before != nil {
					t.Errorf("Before = %v, want nil", got.Before)
				}
				return
			}
			if got.Before == nil {
				t.Fatalf("Before = nil, want %v", tt.want)
			}
			if !got.Before.Equal(tt.want) {
				t.Errorf("Before = %v, want %v", got.Before, tt.want)
			}
		})
	}
}

func TestApplyFiltersCursor(t *testing.T) {
	at := func(sec int) time.Time { return time.Date(2026, 10, 20, 12, 0, sec, 0, time.UTC) }
	cursor := at(30)

	for _, tt := range []struct {
		name  string
		entry Entry
		want  bool
	}{
		{name: "older than the cursor is kept", entry: Entry{Timestamp: at(29)}, want: true},
		{name: "newer than the cursor is dropped", entry: Entry{Timestamp: at(31)}, want: false},
		{
			// The cursor is the last entry the client already rendered, so
			// including it would repeat a row on every page.
			name: "the cursor entry itself is dropped", entry: Entry{Timestamp: at(30)}, want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := applyFilters(tt.entry, Filter{Before: &cursor}); got != tt.want {
				t.Errorf("applyFilters() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyFiltersCursorComposesWithOtherFilters(t *testing.T) {
	cursor := time.Date(2026, 10, 20, 12, 0, 30, 0, time.UTC)
	older := time.Date(2026, 10, 20, 12, 0, 10, 0, time.UTC)

	filter := Filter{Operation: "start", Before: &cursor}

	if !applyFilters(Entry{Timestamp: older, Operation: "start"}, filter) {
		t.Error("an older start should pass both the cursor and the operation filter")
	}
	if applyFilters(Entry{Timestamp: older, Operation: "stop"}, filter) {
		t.Error("the operation filter must still apply when a cursor is set")
	}
}
