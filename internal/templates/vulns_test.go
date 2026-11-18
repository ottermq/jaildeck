package templates

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vulns"
)

type stubAudit map[string]vulns.Summary

func (stubAudit) AuditTemplate(context.Context, string, string) {}
func (stubAudit) ForgetTemplate(string)                         {}

// recordingAudit records each template audited as soon as it was sealed.
type recordingAudit struct {
	mu        sync.Mutex
	audited   []string
	forgotten []string
}

func (r *recordingAudit) ForgetTemplate(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forgotten = append(r.forgotten, name)
}

func (r *recordingAudit) Template(string) vulns.Summary {
	return vulns.Summary{Status: vulns.Unchecked}
}

func (r *recordingAudit) AuditTemplate(_ context.Context, name, root string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audited = append(r.audited, name+" at "+root)
}

func (r *recordingAudit) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.audited...)
}

// Every way a template is sealed audits it at once, through its @base
// (#257): a derive, a prepare and a refresh.
func TestASealedTemplateIsAuditedAtOnce(t *testing.T) {
	t.Run("derive", func(t *testing.T) {
		audit := &recordingAudit{}
		service := NewService(release(), &stubPackages{}, &stubReleases{}, &fakeLogger{})
		service.SetVulnerabilities(audit)
		if _, _, err := service.Derive(context.Background(), "web", "15.1-RELEASE", []string{"nginx"}); err != nil {
			t.Fatal(err)
		}
		service.audits.Wait()
		if got := audit.got(); !slices.Equal(got, []string{"web at /usr/local/jails/templates/web/.zfs/snapshot/base"}) {
			t.Errorf("audited %v", got)
		}
	})
	t.Run("prepare", func(t *testing.T) {
		audit := &recordingAudit{}
		service := NewService(&stubStorage{}, &stubPackages{}, &stubReleases{}, &fakeLogger{})
		service.SetVulnerabilities(audit)
		if _, err := service.Prepare(context.Background(), "15.1-RELEASE", "15.1-RELEASE"); err != nil {
			t.Fatal(err)
		}
		waitIdle(t, service, "15.1-RELEASE")
		service.audits.Wait()
		if got := audit.got(); len(got) != 1 || !strings.HasPrefix(got[0], "15.1-RELEASE at ") || !strings.HasSuffix(got[0], "/.zfs/snapshot/base") {
			t.Errorf("audited %v", got)
		}
	})
	t.Run("refresh", func(t *testing.T) {
		audit := &recordingAudit{}
		_, _, _, service := refreshable(BuiltDerived)
		service.SetVulnerabilities(audit)
		if _, err := service.Refresh(context.Background(), "pg18-p7", "pg18"); err != nil {
			t.Fatal(err)
		}
		waitIdle(t, service, "pg18-p7")
		service.audits.Wait()
		if got := audit.got(); !slices.Equal(got, []string{"pg18-p7 at /usr/local/jails/templates/pg18-p7/.zfs/snapshot/base"}) {
			t.Errorf("audited %v", got)
		}
	})
}

// A template that was not sealed has nothing to audit yet.
func TestAFailedSealAuditsNothing(t *testing.T) {
	audit := &recordingAudit{}
	_, releases, _, service := refreshable(BuiltDerived)
	releases.updateErr = context.DeadlineExceeded
	service.SetVulnerabilities(audit)
	if _, err := service.Refresh(context.Background(), "pg18-p7", "pg18"); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, service, "pg18-p7")
	service.audits.Wait()
	if got := audit.got(); len(got) != 0 {
		t.Errorf("audited %v", got)
	}
}

func (s stubAudit) Template(name string) vulns.Summary {
	if summary, ok := s[name]; ok {
		return summary
	}
	return vulns.Summary{Status: vulns.Unchecked}
}

// Only a template with a tree has an audit: one not yet mounted has no
// packages to list.
func TestListGivesEachTemplateWithATreeItsLastAudit(t *testing.T) {
	storage := release()
	storage.templates = append(storage.templates,
		Template{Name: "pg18", Dataset: "tank/jails/templates/pg18", Mountpoint: "/usr/local/jails/templates/pg18", Ready: true},
		Template{Name: "unmounted", Dataset: "tank/jails/templates/unmounted", Mountpoint: "none"})
	service := NewService(storage, &stubPackages{}, &stubReleases{fetched: []string{"14.3-RELEASE"}}, &fakeLogger{})
	service.SetVulnerabilities(stubAudit{"pg18": {Status: vulns.Vulnerable, Packages: 20, Vulnerable: 5}})

	found, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, tmpl := range found {
		switch tmpl.Name {
		case "pg18":
			seen++
			if tmpl.Vulnerabilities == nil || tmpl.Vulnerabilities.Vulnerable != 5 {
				t.Errorf("pg18: %+v", tmpl.Vulnerabilities)
			}
		case "unmounted":
			seen++
			if tmpl.Vulnerabilities != nil {
				t.Errorf("a template with no tree has %+v", tmpl.Vulnerabilities)
			}
		}
		if tmpl.HasTree() && tmpl.Vulnerabilities == nil {
			t.Errorf("%s has a tree and no audit", tmpl.Name)
		}
	}
	if seen != 2 {
		t.Fatalf("listed %d of the two templates", seen)
	}
}

// A removed template's audit goes with it (#264): a template made later
// under the same name must not show it.
func TestRemoveForgetsTheAudit(t *testing.T) {
	audit := &recordingAudit{}
	service := NewService(removable(), &stubPackages{}, &stubReleases{}, &fakeLogger{})
	service.SetVulnerabilities(audit)
	if _, err := service.Remove(context.Background(), "postgres-16", "postgres-16"); err != nil {
		t.Fatal(err)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if !slices.Equal(audit.forgotten, []string{"postgres-16"}) {
		t.Errorf("forgot %v", audit.forgotten)
	}
}

// freebsd-update says why it failed on standard output (#264); the audit
// entry carries the end of it when standard error is empty.
func TestAFailedStageCarriesItsOutput(t *testing.T) {
	logger := &fakeLogger{}
	service := NewService(&stubStorage{}, &stubPackages{}, &stubReleases{}, logger)
	err := &system.CommandError{Command: "freebsd-update", Err: errors.New("exit status 1"),
		Result: system.CommandResult{ExitCode: 1, Stdout: "Fetching metadata signature...\n\nfiles/abc.gz not found\nFetching files failed.\n"}}
	service.logOperation(context.Background(), OperationUpdate, "x", "freebsd-update ...", err)
	got := logger.entries[0].Error
	if got != "exit status 1: Fetching metadata signature... / files/abc.gz not found / Fetching files failed." {
		t.Errorf("error %q", got)
	}
}
