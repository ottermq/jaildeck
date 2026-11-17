package templates

import (
	"context"
	"testing"

	"github.com/ottermq/jaildeck/internal/vulns"
)

type stubAudit map[string]vulns.Summary

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
