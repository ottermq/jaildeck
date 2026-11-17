package vulns

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"testing"
	"time"
)

type stubSource struct {
	mu         sync.Mutex
	installed  map[string][]Package
	failList   map[string]error
	vulnerable map[string]bool // by package name
	failAudit  error
	dbErr      error
	dbUpdated  time.Time
	audited    [][]Package
}

func (s *stubSource) Installed(_ context.Context, root string) ([]Package, int, error) {
	if err := s.failList[root]; err != nil {
		return nil, 0, err
	}
	return s.installed[root], 0, nil
}

func (s *stubSource) Audit(_ context.Context, pkgs []Package) ([]Finding, error) {
	s.mu.Lock()
	s.audited = append(s.audited, pkgs)
	s.mu.Unlock()
	if s.failAudit != nil {
		return nil, s.failAudit
	}
	out := []Finding{}
	for _, p := range pkgs {
		if s.vulnerable[p.Name] {
			out = append(out, Finding{Name: p.Name, Version: p.Version, Issues: []Issue{{Description: "bad"}}})
		}
	}
	return out, nil
}

func (s *stubSource) Database(context.Context) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "/var/db/pkg/vuln.xml", s.dbUpdated, s.dbErr
}

type stubTargets struct {
	jails, templates []Target
	jailsErr         error
}

func (t stubTargets) Jails(context.Context) ([]Target, error)     { return t.jails, t.jailsErr }
func (t stubTargets) Templates(context.Context) ([]Target, error) { return t.templates, nil }

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func newTestService(src *stubSource, targets stubTargets) *Service {
	s := NewService(src, targets)
	s.now = func() time.Time { return now }
	return s
}

func pkgs(names ...string) []Package {
	out := make([]Package, 0, len(names))
	for _, n := range names {
		out = append(out, Package{Name: n, Version: "1.0"})
	}
	return out
}

func TestAudit(t *testing.T) {
	src := &stubSource{
		installed: map[string][]Package{
			"/":                   pkgs("pkg", "postgresql18-client"),
			"/jails/db":           pkgs("postgresql18-server", "postgresql18-client", "zstd"),
			"/jails/api":          nil, // a Go binary and nothing else
			"/templates/pg18":     pkgs("postgresql18-server"),
			"/templates/15.1-REL": nil,
		},
		failList:   map[string]error{"/jails/broken": errors.New("pkg: Invalid rootdir")},
		vulnerable: map[string]bool{"postgresql18-server": true, "postgresql18-client": true},
		dbUpdated:  now.Add(-7 * time.Hour),
	}
	s := newTestService(src, stubTargets{
		jails:     []Target{{"db", "/jails/db"}, {"api", "/jails/api"}, {"broken", "/jails/broken"}, {"pathless", ""}},
		templates: []Target{{"pg18", "/templates/pg18"}, {"15.1-RELEASE", "/templates/15.1-REL"}},
	})
	snap := s.Audit(context.Background())

	if !snap.Database.Present || snap.Database.Stale || snap.Checked == nil {
		t.Errorf("database %+v, checked %v", snap.Database, snap.Checked)
	}
	check := func(what string, got Report, status Status, packages, vulnerable int) {
		t.Helper()
		if got.Status != status || got.Packages != packages || got.Vulnerable != vulnerable {
			t.Errorf("%s: %s %d/%d (%s), want %s %d/%d", what, got.Status, got.Vulnerable, got.Packages,
				got.Reason, status, vulnerable, packages)
		}
		if (status == Clean || status == Vulnerable) != (got.Checked != nil) {
			t.Errorf("%s: checked is %v with status %s", what, got.Checked, got.Status)
		}
		if got.Findings == nil {
			t.Errorf("%s: findings is null", what)
		}
	}
	check("host", snap.Host, Vulnerable, 2, 1)
	check("db", snap.Jails[0].Report, Vulnerable, 3, 2)
	check("api", snap.Jails[1].Report, Clean, 0, 0)
	check("broken", snap.Jails[2].Report, Unknown, 0, 0)
	check("pathless", snap.Jails[3].Report, Unknown, 0, 0)
	check("pg18", snap.Templates[0].Report, Vulnerable, 1, 1)
	check("15.1-RELEASE", snap.Templates[1].Report, Clean, 0, 0)

	// pkg audit with no names audits the host: a tree with no packages is
	// never audited at all.
	for _, call := range src.audited {
		if len(call) == 0 {
			t.Fatal("pkg audit was called with no packages")
		}
	}
	if len(src.audited) != 3 {
		t.Errorf("audited %d trees, want 3: the host, db and pg18", len(src.audited))
	}

	if got := s.Jail("db"); got.Status != Vulnerable || got.Vulnerable != 2 {
		t.Errorf("Jail(db) is %+v", got)
	}
	if got := s.Jail("created-since"); got.Status != Unchecked {
		t.Errorf("a jail the audit did not include is %s, want unchecked", got.Status)
	}
	if got := s.Template("pg18"); got.Status != Vulnerable {
		t.Errorf("Template(pg18) is %+v", got)
	}
}

func TestBeforeTheFirstAudit(t *testing.T) {
	s := newTestService(&stubSource{}, stubTargets{})
	snap := s.Snapshot()
	if snap.Checked != nil || snap.Host.Status != Unchecked || snap.Host.Reason == "" ||
		snap.Jails == nil || snap.Templates == nil {
		t.Fatalf("before any audit: %+v", snap)
	}
	if s.Jail("any").Status != Unchecked {
		t.Error("a jail before any audit is not unchecked")
	}
}

// No database is unknown everywhere, and nothing is audited against it.
func TestNoDatabase(t *testing.T) {
	for _, dbErr := range []error{
		fmt.Errorf("stat: %w", fs.ErrNotExist),
		errors.New("permission denied"),
	} {
		src := &stubSource{installed: map[string][]Package{"/": pkgs("a"), "/j": pkgs("b")}, dbErr: dbErr}
		snap := newTestService(src, stubTargets{jails: []Target{{"j", "/j"}}}).Audit(context.Background())
		if snap.Database.Present || snap.Database.Updated != nil {
			t.Errorf("%v: database %+v", dbErr, snap.Database)
		}
		if snap.Host.Status != Unknown || snap.Jails[0].Status != Unknown || snap.Host.Reason == "" {
			t.Errorf("%v: host %s, jail %s; want unknown", dbErr, snap.Host.Status, snap.Jails[0].Status)
		}
		if len(src.audited) != 0 {
			t.Errorf("%v: audited without a database", dbErr)
		}
	}
}

func TestStaleDatabase(t *testing.T) {
	src := &stubSource{dbUpdated: now.Add(-StaleAfter - time.Minute)}
	snap := newTestService(src, stubTargets{}).Audit(context.Background())
	if !snap.Database.Stale || !snap.Database.Present {
		t.Fatalf("a database %s old: %+v", StaleAfter+time.Minute, snap.Database)
	}
}

func TestAuditFailureIsUnknown(t *testing.T) {
	src := &stubSource{installed: map[string][]Package{"/": pkgs("a")}, failAudit: errors.New("exit status 70"),
		dbUpdated: now}
	snap := newTestService(src, stubTargets{}).Audit(context.Background())
	if snap.Host.Status != Unknown || snap.Host.Reason == "" {
		t.Fatalf("a failed audit is %s (%q), want unknown", snap.Host.Status, snap.Host.Reason)
	}
}

// Jails that could not be listed are left out, so each reads as unchecked
// rather than clean; the templates are still audited.
func TestJailsUnlisted(t *testing.T) {
	src := &stubSource{installed: map[string][]Package{"/t": pkgs("a")}, dbUpdated: now}
	s := newTestService(src, stubTargets{jailsErr: errors.New("jls failed"), templates: []Target{{"t", "/t"}}})
	snap := s.Audit(context.Background())
	if len(snap.Jails) != 0 || len(snap.Templates) != 1 || s.Jail("x").Status != Unchecked {
		t.Fatalf("jails %v, templates %v", snap.Jails, snap.Templates)
	}
}

func TestBoundedReason(t *testing.T) {
	long := errors.New(string(make([]byte, 1000)))
	src := &stubSource{failList: map[string]error{"/": long}, dbUpdated: now}
	snap := newTestService(src, stubTargets{}).Audit(context.Background())
	if n := len(snap.Host.Reason); n > 310 {
		t.Errorf("a reason %d bytes long", n)
	}
}

func TestDue(t *testing.T) {
	src := &stubSource{dbUpdated: now.Add(-time.Hour)}
	s := newTestService(src, stubTargets{})
	ctx := context.Background()
	if !s.due(ctx, time.Hour) {
		t.Fatal("not due before the first audit")
	}
	s.Audit(ctx)
	if s.due(ctx, time.Hour) {
		t.Fatal("due right after an audit, with the same database")
	}

	src.mu.Lock()
	src.dbUpdated = now
	src.mu.Unlock()
	if !s.due(ctx, time.Hour) {
		t.Fatal("not due after the database changed")
	}
	s.Audit(ctx)

	src.mu.Lock()
	src.dbErr = fs.ErrNotExist
	src.mu.Unlock()
	if !s.due(ctx, time.Hour) {
		t.Fatal("not due after the database went away")
	}
	s.Audit(ctx)
	if s.due(ctx, time.Hour) {
		t.Fatal("due again while the database is still missing")
	}

	s.now = func() time.Time { return now.Add(time.Hour) }
	if !s.due(ctx, time.Hour) {
		t.Fatal("not due an hour after the last audit")
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	s := newTestService(&stubSource{dbUpdated: now}, stubTargets{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, time.Millisecond, time.Hour); close(done) }()
	for s.Snapshot().Checked == nil {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
