package audit

import (
	"context"
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
