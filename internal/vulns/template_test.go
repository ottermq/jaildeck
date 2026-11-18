package vulns

import (
	"context"
	"io/fs"
	"sync"
	"testing"
	"time"
)

// A template sealed between two audits is audited at once, beside the
// others, rather than reading unchecked until the next (#257).
func TestAuditTemplateAddsOneTemplate(t *testing.T) {
	src := &stubSource{
		installed: map[string][]Package{
			"/templates/pg18":          pkgs("postgresql18-server", "expat"),
			"/templates/pg18-new/base": pkgs("postgresql18-server"),
		},
		vulnerable: map[string]bool{"expat": true},
		dbUpdated:  now.Add(-time.Hour),
	}
	s := newTestService(src, stubTargets{templates: []Target{{"pg18", "/templates/pg18"}}})
	before := s.Audit(context.Background())

	s.AuditTemplate(context.Background(), "pg18-new", "/templates/pg18-new/base")

	if got := s.Template("pg18-new"); got.Status != Clean || got.Packages != 1 || got.Checked == nil {
		t.Errorf("new template %+v", got)
	}
	if got := s.Template("pg18"); got.Status != Vulnerable || got.Vulnerable != 1 {
		t.Errorf("the others changed: %+v", got)
	}
	// The published snapshot is never changed in place, and the time of the
	// last full audit is still that audit's.
	if len(before.Templates) != 1 {
		t.Errorf("the earlier snapshot changed: %+v", before.Templates)
	}
	if after := s.Snapshot(); *after.Checked != *before.Checked || len(after.Templates) != 2 {
		t.Errorf("after %+v", after)
	}

	// Audited again, replaced rather than listed twice.
	src.vulnerable["postgresql18-server"] = true
	s.AuditTemplate(context.Background(), "pg18-new", "/templates/pg18-new/base")
	if got := s.Template("pg18-new"); got.Status != Vulnerable || len(s.Snapshot().Templates) != 2 {
		t.Errorf("again: %+v, %d templates", got, len(s.Snapshot().Templates))
	}
}

func TestAuditTemplateWithoutADatabase(t *testing.T) {
	src := &stubSource{dbErr: fs.ErrNotExist,
		installed: map[string][]Package{"/templates/x": pkgs("expat")}}
	s := newTestService(src, stubTargets{})
	s.AuditTemplate(context.Background(), "x", "/templates/x")
	if got := s.Template("x"); got.Status != Unknown || got.Checked != nil {
		t.Errorf("got %+v", got)
	}
	if len(src.audited) != 0 {
		t.Errorf("audited without a database")
	}
}

// Beside a full audit, never at the same time: run together, the race
// detector and the result both say so.
func TestAuditTemplateWaitsForAFullAudit(t *testing.T) {
	src := &stubSource{
		installed: map[string][]Package{"/a": pkgs("expat"), "/b": pkgs("zstd")},
		dbUpdated: now,
	}
	s := newTestService(src, stubTargets{templates: []Target{{"a", "/a"}}})
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(2)
		go func() { defer wg.Done(); s.Audit(context.Background()) }()
		go func() { defer wg.Done(); s.AuditTemplate(context.Background(), "b", "/b") }()
	}
	wg.Wait()
	// Whichever ran last, a stays; b is there unless a full audit ran last.
	if got := s.Template("a"); got.Status != Clean {
		t.Errorf("a: %+v", got)
	}
}
