package fake

import (
	"context"
	"errors"
	"time"

	"github.com/ottermq/jaildeck/internal/vulns"
)

// Vulns is a host with one vulnerable package, a jail with two, a clean jail
// and a template with one, so the interface has every audit state to show
// without a FreeBSD box (#230). Anything created while it runs has no
// packages, and is clean once audited.
type Vulns struct{}

func NewVulns() Vulns { return Vulns{} }

var fakePackages = map[string][]vulns.Package{
	"/":                                 {{Name: "pkg", Version: "2.9.0"}, {Name: "python312", Version: "3.12.15"}},
	"/fake/jail/nginx":                  {{Name: "nginx", Version: "1.28.0"}, {Name: "pcre2", Version: "10.45"}},
	"/fake/jail/redis":                  {{Name: "redis", Version: "8.0.2"}, {Name: "expat", Version: "2.8.4"}, {Name: "python312", Version: "3.12.15"}},
	"/fake/jail/templates/15.1-RELEASE": {{Name: "expat", Version: "2.8.4"}},
}

var fakeIssues = map[string]vulns.Finding{
	"python312": {Issues: []vulns.Issue{{
		Description: "Python -- imaplib module command injection",
		CVEs:        []string{"CVE-2025-15366"}, URL: "https://vuxml.FreeBSD.org/freebsd/0be929a5-2e0f-11f1-88c7-00a098b42aeb.html",
		Affected: []string{">= 0"},
	}}, ReverseDependencies: []string{"py312-packaging"}},
	"expat": {Issues: []vulns.Issue{{
		Description: "expat -- multiple vulnerabilities",
		CVEs:        []string{"CVE-2026-0001", "CVE-2026-0002"}, URL: "https://vuxml.FreeBSD.org/freebsd/00000000-0000-0000-0000-000000000000.html",
		Affected: []string{"< 2.8.5"},
	}}, ReverseDependencies: []string{}},
}

func (Vulns) Installed(_ context.Context, root string) ([]vulns.Package, int, error) {
	if root == "" {
		return nil, 0, errors.New("no root")
	}
	return fakePackages[root], 0, nil
}

func (Vulns) Audit(_ context.Context, pkgs []vulns.Package) ([]vulns.Finding, error) {
	if len(pkgs) == 0 {
		return nil, errors.New("no packages to audit")
	}
	out := []vulns.Finding{}
	for _, p := range pkgs {
		if f, ok := fakeIssues[p.Name]; ok {
			f.Name, f.Version = p.Name, p.Version
			out = append(out, f)
		}
	}
	return out, nil
}

// Database was fetched a few hours ago, as the daily security run would.
func (Vulns) Database(context.Context) (string, time.Time, error) {
	return "/var/db/pkg/vuln.xml", time.Now().Add(-5 * time.Hour).Truncate(time.Hour), nil
}

var _ vulns.Source = Vulns{}
