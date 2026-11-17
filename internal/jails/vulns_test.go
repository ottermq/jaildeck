package jails

import (
	"context"
	"testing"

	"github.com/ottermq/jaildeck/internal/vulns"
)

type stubAudit map[string]vulns.Summary

func (s stubAudit) Jail(name string) vulns.Summary {
	if summary, ok := s[name]; ok {
		return summary
	}
	return vulns.Summary{Status: vulns.Unchecked}
}

func auditedService() *JailService {
	svc := NewJailService(Dependencies{System: listingSystem{jails: []Jail{
		{Name: "web", Status: JailStatusRunning, Path: "/jails/web"},
		{Name: "db", Status: JailStatusStopped, Path: "/jails/db"},
		{Name: "new", Status: JailStatusStopped, Path: "/jails/new"},
	}}, Audit: &fakeLogger{}})
	svc.SetVulnerabilities(stubAudit{
		"web": {Status: vulns.Clean, Packages: 12},
		"db":  {Status: vulns.Vulnerable, Packages: 20, Vulnerable: 5},
	})
	return svc
}

// The listing reads the last audit and runs none: a jail created since is
// unchecked, not clean.
func TestListGivesEachJailItsLastAudit(t *testing.T) {
	found, err := auditedService().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]vulns.Summary{
		"web": {Status: vulns.Clean, Packages: 12},
		"db":  {Status: vulns.Vulnerable, Packages: 20, Vulnerable: 5},
		"new": {Status: vulns.Unchecked},
	}
	for _, jail := range found {
		if jail.Vulnerabilities == nil || *jail.Vulnerabilities != want[jail.Name] {
			t.Errorf("%s: %+v, want %+v", jail.Name, jail.Vulnerabilities, want[jail.Name])
		}
	}
}

func TestGetGivesTheJailItsLastAudit(t *testing.T) {
	jail, err := auditedService().Get(context.Background(), JailName("db"))
	if err != nil {
		t.Fatal(err)
	}
	if jail.Vulnerabilities == nil || jail.Vulnerabilities.Vulnerable != 5 {
		t.Fatalf("db: %+v", jail.Vulnerabilities)
	}
}

func TestNoAuditLeavesJailsWithout(t *testing.T) {
	svc := NewJailService(Dependencies{System: listingSystem{jails: []Jail{{Name: "web", Path: "/jails/web"}}},
		Audit: &fakeLogger{}})
	found, err := svc.List(context.Background())
	if err != nil || found[0].Vulnerabilities != nil {
		t.Fatalf("got %+v, %v; want no vulnerabilities field", found, err)
	}
}
