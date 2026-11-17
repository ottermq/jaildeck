package metrics

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/vulns"
)

func someAudit() vulns.Snapshot {
	checked := time.Unix(1790000000, 0)
	updated := time.Unix(1789990000, 0)
	report := func(status vulns.Status, packages, vulnerable int) vulns.Report {
		r := vulns.Report{Summary: vulns.Summary{Status: status, Packages: packages, Vulnerable: vulnerable},
			Findings: []vulns.Finding{}}
		if status == vulns.Clean || status == vulns.Vulnerable {
			r.Checked = &checked
		}
		if vulnerable > 0 {
			r.Findings = []vulns.Finding{{Name: "postgresql18-server", Version: "18.6",
				Issues: []vulns.Issue{{Description: "PostgreSQL -- multiple vulnerabilities", CVEs: []string{"CVE-2026-6460"}}}}}
		}
		return r
	}
	return vulns.Snapshot{
		Checked:  &checked,
		Database: vulns.Database{Path: "/var/db/pkg/vuln.xml", Present: true, Updated: &updated},
		Host:     report(vulns.Vulnerable, 400, 2),
		Jails: []vulns.Named{
			{Name: "db", Report: report(vulns.Vulnerable, 20, 5)},
			{Name: "api", Report: report(vulns.Clean, 0, 0)},
			{Name: "broken", Report: report(vulns.Unknown, 0, 0)},
		},
		Templates: []vulns.Named{{Name: "pg18", Report: report(vulns.Vulnerable, 20, 4)}},
	}
}

type stubAudit vulns.Snapshot

func (s stubAudit) Snapshot() vulns.Snapshot { return vulns.Snapshot(s) }

func TestVulnerabilityMetrics(t *testing.T) {
	report := aReport()
	report.Vulnerabilities = countVulnerable(someAudit())
	body := render(t, report)
	checkFormat(t, body)

	requireLine(t, body, "jaildeck_vulnerability_last_audit_timestamp_seconds 1.79e+09")
	requireLine(t, body, "jaildeck_vulnerability_database_timestamp_seconds 1.78999e+09")
	requireLine(t, body, "jaildeck_host_vulnerable_packages 2")
	requireLine(t, body, `jaildeck_jail_vulnerable_packages{jail="db"} 5`)
	requireLine(t, body, `jaildeck_jail_vulnerable_packages{jail="api"} 0`)
	requireLine(t, body, `jaildeck_template_vulnerable_packages{template="pg18"} 4`)
	// Unknown is not 0: left out, so an alert never clears on a guess.
	if strings.Contains(body, `jail="broken"`) {
		t.Error("an unknown jail written as a count")
	}
}

// Before the first audit, and without a database, nothing is written that
// would read as "no vulnerable packages".
func TestVulnerabilityMetricsBeforeAnAudit(t *testing.T) {
	report := aReport()
	report.Vulnerabilities = countVulnerable(vulns.NewService(nil, nil).Snapshot())
	body := render(t, report)
	checkFormat(t, body)
	for _, absent := range []string{"jaildeck_vulnerability_", "_vulnerable_packages"} {
		if strings.Contains(body, absent) {
			t.Errorf("%s written before any audit", absent)
		}
	}
}

// The report carries counts, never the findings: the dashboard asks every
// 5 s, and a database jail has fifty CVEs.
func TestReportCountsVulnerabilities(t *testing.T) {
	s := NewService(aHost(), nil, nil, nil)
	s.SetVulnerabilities(stubAudit(someAudit()))
	report, err := s.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v := report.Vulnerabilities
	if v == nil || v.Host.Vulnerable != 2 || len(v.Jails) != 3 || v.Jails[0].Vulnerable != 5 ||
		v.Templates[0].Name != "pg18" || !v.Database.Present {
		t.Fatalf("report.Vulnerabilities is %+v", v)
	}
}
