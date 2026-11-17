package vulns

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseInstalled(t *testing.T) {
	out := strings.Join([]string{
		"postgresql18-server\t18.6",
		"icu\t76.1,1",
		"zstd\t1.5.7_2",
		"py312-packaging\t25.0",
		"gtk+\t2.24.33_1",
		// Not shaped like a package: never passed on.
		"-f\t1.0",     // would be an option
		"evil\t-1",    // a version that is an option
		"spa ce\t1.0", // a space
		"quote\t1'0",  // a quote
		"$(id)\t1.0",  // a shell
		"no-tab-1.0",  // the old %n-%v shape
		"a\tb\tc",     // two tabs
		"\t1.0",       // no name
		"name\t",      // no version
		"x\t1.0\r",    // a carriage return
		strings.Repeat("n", 129) + "\t1.0",
		"",
	}, "\n")
	pkgs, skipped, err := ParseInstalled([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"postgresql18-server-18.6", "icu-76.1,1", "zstd-1.5.7_2", "py312-packaging-25.0", "gtk+-2.24.33_1"}
	if len(pkgs) != len(want) {
		t.Fatalf("got %v, want %v", pkgs, want)
	}
	for i, p := range pkgs {
		if p.String() != want[i] {
			t.Errorf("package %d is %q, want %q", i, p, want[i])
		}
	}
	if skipped != 11 {
		t.Errorf("skipped %d lines, want 11", skipped)
	}
}

func TestParseInstalledEmpty(t *testing.T) {
	pkgs, skipped, err := ParseInstalled(nil)
	if err != nil || len(pkgs) != 0 || skipped != 0 {
		t.Fatalf("got %v, %d, %v; want nothing", pkgs, skipped, err)
	}
}

func TestParseInstalledRefusesTooMany(t *testing.T) {
	var b strings.Builder
	for i := range MaxPackages + 1 {
		fmt.Fprintf(&b, "p%d\t1.0\n", i)
	}
	if _, _, err := ParseInstalled([]byte(b.String())); !errors.Is(err, ErrTooManyPackages) {
		t.Fatalf("got %v, want ErrTooManyPackages", err)
	}
}

// Trimmed from pkg 2.9.0's output on a FreeBSD 15.1 host.
const auditOutput = `{"pkg_count":2,"packages":{"python312":{"version":"3.12.15","issue_count":1,"issues":[{"Affected versions":[">= 0"],"description":"Python -- poplib module command injection","cve":["CVE-2025-15367"],"url":"https://vuxml.FreeBSD.org/freebsd/6d3488ae.html"}],"reverse dependencies":["py312-packaging"]},"postgresql18-server":{"version":"18.6","issue_count":1,"issues":[{"Affected versions":["< 18.7"],"description":"PostgreSQL -- multiple vulnerabilities","cve":["CVE-2026-6460","CVE-2026-6464"],"url":"https://vuxml.FreeBSD.org/freebsd/1a672925.html"}],"reverse dependencies":[]}}}`

func TestParseAudit(t *testing.T) {
	findings, err := ParseAudit([]byte(auditOutput))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Name != "postgresql18-server" || findings[1].Name != "python312" {
		t.Fatalf("got %+v, want postgresql18-server then python312", findings)
	}
	pg := findings[0]
	if pg.Version != "18.6" || len(pg.Issues) != 1 || len(pg.Issues[0].CVEs) != 2 ||
		pg.Issues[0].Affected[0] != "< 18.7" || pg.ReverseDependencies == nil {
		t.Errorf("postgresql18-server read as %+v", pg)
	}
	if findings[1].ReverseDependencies[0] != "py312-packaging" {
		t.Errorf("python312's reverse dependencies read as %v", findings[1].ReverseDependencies)
	}
}

func TestParseAuditNothingFound(t *testing.T) {
	findings, err := ParseAudit([]byte(`{"pkg_count":0}`))
	if err != nil || findings == nil || len(findings) != 0 {
		t.Fatalf("got %v, %v; want an empty list", findings, err)
	}
}

// A failure is never read as nothing found.
func TestParseAuditRefusesWhatIsNotAnAudit(t *testing.T) {
	for _, out := range []string{
		"",
		"pkg: vulnxml file /var/db/pkg/vuln.xml does not exist. Try running 'pkg audit -F' first",
		`{}`,
		`{"packages":{}}`,
		`pkg_count = 0;`,
		`{"pkg_count":2,"packages":{"a":{"version":"1"}}}`,
	} {
		if _, err := ParseAudit([]byte(out)); err == nil {
			t.Errorf("%q read as an audit", out)
		}
	}
}
