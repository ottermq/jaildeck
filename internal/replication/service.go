package replication

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
)

// Source reads zrepl's replication cursors on every pool.
type Source interface {
	ReplicationCursors(ctx context.Context) ([]Cursor, error)
}

// Every is how often the cursors are read. A read lists bookmarks only,
// which costs a few hundredths of a second, and zrepl's own intervals are
// minutes at the shortest.
const Every = time.Minute

// Service keeps the last read. Requests and scrapes never read the cursors
// themselves.
type Service struct {
	source Source
	now    func() time.Time

	mu   sync.RWMutex
	snap Snapshot
}

func NewService(source Source) *Service {
	return &Service{source: source, now: time.Now,
		snap: Snapshot{Status: Unchecked, Reason: "the first read has not finished", Jobs: []Job{}}}
}

// Snapshot is the last read. Nothing in it changes after it is published.
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Run reads now, then every interval, until ctx is done.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	s.Read(ctx)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Read(ctx)
		}
	}
}

// Read reads the cursors and publishes them. A failed read is unknown, with
// no jobs: the last good answer is not kept, because ages computed from it
// would look current. Checked stays at the last success.
func (s *Service) Read(ctx context.Context) Snapshot {
	cursors, err := s.source.ReplicationCursors(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		common.Logger(ctx, "replication").Warn("could not read replication cursors", slog.Any("err", err))
		s.snap = Snapshot{Status: Unknown, Checked: s.snap.Checked, Jobs: []Job{},
			Reason: bounded("zrepl's replication cursors could not be read: " + err.Error())}
		return s.snap
	}
	now := s.now()
	snap := Snapshot{Status: None, Checked: &now, Jobs: group(cursors)}
	if len(snap.Jobs) > 0 {
		snap.Status = Replicating
	}
	s.snap = snap
	return snap
}

// group sorts cursors into jobs. A dataset may briefly carry two cursors
// for one job, while zrepl moves it on; the newer wins.
func group(cursors []Cursor) []Job {
	newest := map[string]map[string]time.Time{}
	for _, c := range cursors {
		if newest[c.Job] == nil {
			newest[c.Job] = map[string]time.Time{}
		}
		if t, ok := newest[c.Job][c.Dataset]; !ok || c.Snapshot.After(t) {
			newest[c.Job][c.Dataset] = c.Snapshot
		}
	}
	jobs := make([]Job, 0, len(newest))
	for name, datasets := range newest {
		job := Job{Name: name, Datasets: make([]Dataset, 0, len(datasets))}
		for ds, t := range datasets {
			job.Datasets = append(job.Datasets, Dataset{Name: ds, Replicated: t})
			if job.Oldest.IsZero() || t.Before(job.Oldest) {
				job.Oldest = t
			}
		}
		sort.Slice(job.Datasets, func(i, j int) bool { return job.Datasets[i].Name < job.Datasets[j].Name })
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Name < jobs[j].Name })
	return jobs
}

func bounded(s string) string {
	const limit = 300
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
