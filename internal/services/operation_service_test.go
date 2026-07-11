package services

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ottermq/jaildeck/internal/operations"
)

type fakeReader struct {
	gotLimit  int
	gotFilter operations.Filter
	entries   []operations.Entry
	err       error
}

func (f *fakeReader) Recent(ctx context.Context, limit int, filter operations.Filter) ([]operations.Entry, error) {
	f.gotLimit = limit
	f.gotFilter = filter
	return f.entries, f.err
}

func boolPtr(b bool) *bool { return &b }

func TestOperationService_Recent_BuildsFilter(t *testing.T) {
	tests := []struct {
		name          string
		mapFilter     map[string]string
		wantOperation string
		wantSuccess   *bool
		wantTargets   []string
	}{
		{
			name:      "empty filter map",
			mapFilter: map[string]string{},
		},
		{
			name:          "operation is lowercased",
			mapFilter:     map[string]string{"operation": "START"},
			wantOperation: "start",
		},
		{
			name:        "success true",
			mapFilter:   map[string]string{"success": "true"},
			wantSuccess: boolPtr(true),
		},
		{
			name:        "success false",
			mapFilter:   map[string]string{"success": "false"},
			wantSuccess: boolPtr(false),
		},
		{
			name:      "success invalid value is ignored",
			mapFilter: map[string]string{"success": "maybe"},
		},
		{
			name:        "single target",
			mapFilter:   map[string]string{"targets": "ottermq"},
			wantTargets: []string{"ottermq"},
		},
		{
			name:        "multiple targets with spacing",
			mapFilter:   map[string]string{"targets": "ottermq, goodiesdb"},
			wantTargets: []string{"ottermq", "goodiesdb"},
		},
		{
			name:        "embedded empty elements are dropped",
			mapFilter:   map[string]string{"targets": "ottermq,,goodiesdb,"},
			wantTargets: []string{"ottermq", "goodiesdb"},
		},
		{
			// Regression test for #69: a whitespace-only value must be treated
			// as "no filter", not as a filter that matches nothing.
			name:      "whitespace-only targets is treated as no filter",
			mapFilter: map[string]string{"targets": " "},
		},
		{
			// Regression test for #69: a lone comma used to panic.
			name:      "comma-only targets is treated as no filter",
			mapFilter: map[string]string{"targets": ","},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{}
			svc := NewOperationService(reader)

			if _, err := svc.Recent(context.Background(), 50, tt.mapFilter); err != nil {
				t.Fatalf("Recent() error = %v", err)
			}

			got := reader.gotFilter
			if got.Operation != tt.wantOperation {
				t.Errorf("Operation = %q, want %q", got.Operation, tt.wantOperation)
			}
			if !slices.Equal(got.Targets, tt.wantTargets) {
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
	svc := NewOperationService(reader)

	if _, err := svc.Recent(context.Background(), 17, map[string]string{}); err != nil {
		t.Fatalf("Recent() error = %v", err)
	}

	if reader.gotLimit != 17 {
		t.Errorf("limit passed to reader = %d, want 17", reader.gotLimit)
	}
}

func TestOperationService_Recent_ReturnsReaderResult(t *testing.T) {
	reader := &fakeReader{
		entries: []operations.Entry{{Operation: "start", Target: "ottermq", Success: true}},
	}
	svc := NewOperationService(reader)

	got, err := svc.Recent(context.Background(), 50, map[string]string{})
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
	svc := NewOperationService(reader)

	_, err := svc.Recent(context.Background(), 50, map[string]string{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Recent() error = %v, want %v", err, wantErr)
	}
}
