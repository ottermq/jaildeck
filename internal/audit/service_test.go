package audit

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"testing"
)

type fakeReader struct {
	gotLimit  int
	gotFilter Filter
	entries   []Entry
	err       error
}

func (f *fakeReader) Recent(ctx context.Context, limit int, filter Filter) ([]Entry, error) {
	f.gotLimit = limit
	f.gotFilter = filter
	return f.entries, f.err
}

func boolPtr(b bool) *bool { return &b }

func TestParseFilter(t *testing.T) {
	tests := []struct {
		name          string
		query         url.Values
		wantOperation string
		wantSuccess   *bool
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
			name:        "success true",
			query:       url.Values{"success": {"true"}},
			wantSuccess: boolPtr(true),
		},
		{
			name:        "success false",
			query:       url.Values{"success": {"false"}},
			wantSuccess: boolPtr(false),
		},
		{
			name:  "success invalid value is ignored",
			query: url.Values{"success": {"maybe"}},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{}
			svc := NewAuditService(reader)
			filter := parseFilter(tt.query)
			if _, err := svc.Recent(context.Background(), 50, filter); err != nil {
				t.Fatalf("Recent() error = %v", err)
			}

			got := reader.gotFilter

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
		})
	}
}

func TestOperationService_Recent_PassesLimitThrough(t *testing.T) {
	reader := &fakeReader{}
	svc := NewAuditService(reader)

	if _, err := svc.Recent(context.Background(), 17, Filter{}); err != nil {
		t.Fatalf("Recent() error = %v", err)
	}

	if reader.gotLimit != 17 {
		t.Errorf("limit passed to reader = %d, want 17", reader.gotLimit)
	}
}

func TestOperationService_Recent_ReturnsReaderResult(t *testing.T) {
	reader := &fakeReader{
		entries: []Entry{{Operation: "start", Target: "ottermq", Success: true}},
	}
	svc := NewAuditService(reader)

	got, err := svc.Recent(context.Background(), 50, Filter{})
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if len(got) != 1 || got[0].Target != "ottermq" {
		t.Errorf("Recent() = %#v, want one entry with Target %q", got, "ottermq")
	}
}

func TestOperationService_Recent_PropagatesReaderError(t *testing.T) {
	wantErr := errors.New("boom")
	reader := &fakeReader{err: wantErr}
	svc := NewAuditService(reader)

	_, err := svc.Recent(context.Background(), 50, Filter{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Recent() error = %v, want %v", err, wantErr)
	}
}
