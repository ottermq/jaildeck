package replication

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubSource struct {
	cursors []Cursor
	err     error
}

func (s *stubSource) ReplicationCursors(context.Context) ([]Cursor, error) { return s.cursors, s.err }

var t0 = time.Date(2026, 11, 17, 17, 0, 0, 0, time.UTC)

func TestUncheckedUntilRead(t *testing.T) {
	s := NewService(&stubSource{})
	if snap := s.Snapshot(); snap.Status != Unchecked || snap.Checked != nil || snap.Jobs == nil {
		t.Fatalf("got %+v", snap)
	}
}

func TestReadGroupsByJob(t *testing.T) {
	src := &stubSource{cursors: []Cursor{
		{Dataset: "tank/jails/b", Job: "push", Snapshot: t0.Add(-15 * time.Minute)},
		{Dataset: "tank/jails/a", Job: "push", Snapshot: t0.Add(-40 * time.Minute)},
		{Dataset: "tank/offsite", Job: "source", Snapshot: t0.Add(-2 * time.Hour)},
		// Two cursors for one dataset and job, while zrepl moves it on:
		// the newer wins.
		{Dataset: "tank/jails/b", Job: "push", Snapshot: t0.Add(-30 * time.Minute)},
	}}
	s := NewService(src)
	s.now = func() time.Time { return t0 }
	snap := s.Read(context.Background())
	if snap.Status != Replicating || snap.Checked == nil || !snap.Checked.Equal(t0) {
		t.Fatalf("got %+v", snap)
	}
	if len(snap.Jobs) != 2 || snap.Jobs[0].Name != "push" || snap.Jobs[1].Name != "source" {
		t.Fatalf("jobs %+v", snap.Jobs)
	}
	push := snap.Jobs[0]
	if len(push.Datasets) != 2 || push.Datasets[0].Name != "tank/jails/a" {
		t.Fatalf("datasets %+v", push.Datasets)
	}
	if !push.Datasets[1].Replicated.Equal(t0.Add(-15 * time.Minute)) {
		t.Errorf("the older cursor won: %v", push.Datasets[1].Replicated)
	}
	if !push.Oldest.Equal(t0.Add(-40 * time.Minute)) {
		t.Errorf("oldest %v", push.Oldest)
	}
}

func TestNoCursorsIsNone(t *testing.T) {
	s := NewService(&stubSource{cursors: []Cursor{}})
	if snap := s.Read(context.Background()); snap.Status != None || snap.Checked == nil || len(snap.Jobs) != 0 {
		t.Fatalf("got %+v", snap)
	}
}

// A failed read is unknown, never none, and drops the last answer rather
// than serve ages that would look current. Checked stays at the last
// success, so its age says how stale the answer is.
func TestFailedReadIsUnknown(t *testing.T) {
	src := &stubSource{cursors: []Cursor{{Dataset: "tank/a", Job: "push", Snapshot: t0}}}
	s := NewService(src)
	s.now = func() time.Time { return t0 }
	s.Read(context.Background())

	src.err = errors.New("zfs: no such pool")
	s.now = func() time.Time { return t0.Add(time.Minute) }
	snap := s.Read(context.Background())
	if snap.Status != Unknown || len(snap.Jobs) != 0 || snap.Reason == "" {
		t.Fatalf("got %+v", snap)
	}
	if snap.Checked == nil || !snap.Checked.Equal(t0) {
		t.Errorf("checked %v, want the last success", snap.Checked)
	}
}
