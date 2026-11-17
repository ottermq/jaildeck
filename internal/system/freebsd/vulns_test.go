package freebsd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vulns"
)

// auditRunner answers pkg with a scripted result and records what it ran.
type auditRunner struct {
	result system.CommandResult
	err    error
	ran    []system.Command
}

func (r *auditRunner) Run(_ context.Context, cmd system.Command) (system.CommandResult, error) {
	r.ran = append(r.ran, cmd)
	return r.result, r.err
}

func auditAdapter(r *auditRunner) *Adapter {
	a := NewAdapter(r)
	a.vulnDatabase = "/var/db/pkg/vuln.xml"
	return a
}

// Every pkg Jail Deck runs against a jail's database runs as nobody, with no
// supplementary groups (JD-061).
func checkAsNobody(t *testing.T, cmd system.Command) {
	t.Helper()
	if cmd.Credential == nil || cmd.Credential.Uid == 0 || cmd.Credential.Gid == 0 ||
		cmd.Credential.Groups == nil || len(cmd.Credential.Groups) != 0 {
		t.Fatalf("%s ran with credential %+v, want nobody's and no groups", cmd, cmd.Credential)
	}
}

func TestInstalled(t *testing.T) {
	r := &auditRunner{result: system.CommandResult{Stdout: "expat\t2.8.4\npostgresql18-server\t18.6\n-x\t1\n"}}
	pkgs, skipped, err := auditAdapter(r).Installed(context.Background(), "/usr/local/jails/containers/db")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 || pkgs[1].String() != "postgresql18-server-18.6" || skipped != 1 {
		t.Fatalf("got %v, skipped %d", pkgs, skipped)
	}
	cmd := r.ran[0]
	if got := cmd.String(); got != "pkg -r /usr/local/jails/containers/db query %n\t%v" {
		t.Errorf("ran %q", got)
	}
	checkAsNobody(t, cmd)
	if cmd.MaxOutput == 0 || cmd.Timeout == 0 {
		t.Errorf("unbounded: %+v", cmd)
	}
}

func TestInstalledNoDatabase(t *testing.T) {
	// What pkg 2.9 does for a tree with no package database.
	r := &auditRunner{result: system.CommandResult{Stderr: "pkg: No packages installed\n"}}
	pkgs, _, err := auditAdapter(r).Installed(context.Background(), "/t")
	if err != nil || len(pkgs) != 0 {
		t.Fatalf("got %v, %v; want none", pkgs, err)
	}
}

func TestInstalledRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		root   string
		runner *auditRunner
	}{
		"a relative root": {"jails/db", &auditRunner{}},
		"an option":       {"-c", &auditRunner{}},
		"pkg failing": {"/nonexistent", &auditRunner{
			result: system.CommandResult{ExitCode: 1, Stderr: "pkg: Invalid rootdir"}, err: errors.New("exit status 1")}},
		"a list over the cap": {"/t", &auditRunner{result: system.CommandResult{Stdout: "a\t1\n", StdoutTruncated: true}}},
	} {
		if _, _, err := auditAdapter(tc.runner).Installed(context.Background(), tc.root); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestAuditCommand(t *testing.T) {
	r := &auditRunner{
		// Found something: pkg exits 1 with the audit on stdout.
		result: system.CommandResult{ExitCode: 1,
			Stdout: `{"pkg_count":1,"packages":{"expat":{"version":"2.8.4","issues":[{"Affected versions":["< 2.8.5"],"description":"expat","cve":["CVE-1"],"url":"u"}],"reverse dependencies":[]}}}`},
		err: errors.New("exit status 1"),
	}
	findings, err := auditAdapter(r).Audit(context.Background(),
		[]vulns.Package{{Name: "expat", Version: "2.8.4"}, {Name: "zstd", Version: "1.5.7_2"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Name != "expat" {
		t.Fatalf("got %+v", findings)
	}
	cmd := r.ran[0]
	want := []string{"audit", "--raw=json-compact", "-f", "/var/db/pkg/vuln.xml", "--", "expat-2.8.4", "zstd-1.5.7_2"}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") || cmd.Name != "pkg" {
		t.Errorf("ran %s %v, want pkg %v", cmd.Name, cmd.Args, want)
	}
	checkAsNobody(t, cmd)
}

// pkg audit given no names audits the host: never run with none.
func TestAuditRefusesNoPackages(t *testing.T) {
	r := &auditRunner{result: system.CommandResult{Stdout: `{"pkg_count":0}`}}
	if _, err := auditAdapter(r).Audit(context.Background(), nil); !errors.Is(err, errNoPackages) {
		t.Fatalf("got %v, want errNoPackages", err)
	}
	if len(r.ran) != 0 {
		t.Fatal("pkg audit ran with no packages")
	}
}

func TestAuditRefusesAPackageNotShapedLikeOne(t *testing.T) {
	r := &auditRunner{}
	_, err := auditAdapter(r).Audit(context.Background(), []vulns.Package{{Name: "-F", Version: "1"}})
	if err == nil || len(r.ran) != 0 {
		t.Fatalf("got %v after %d runs; want refused before running", err, len(r.ran))
	}
}

func TestAuditFailures(t *testing.T) {
	for name, r := range map[string]*auditRunner{
		// The database missing: exit 1 like a finding, but no audit on
		// stdout. Never read as nothing found.
		"no database": {result: system.CommandResult{ExitCode: 1,
			Stderr: "pkg: vulnxml file /var/db/pkg/vuln.xml does not exist"}, err: errors.New("exit status 1")},
		"another exit": {result: system.CommandResult{ExitCode: 70, Stdout: `{"pkg_count":0}`}, err: errors.New("exit status 70")},
		"timed out":    {result: system.CommandResult{ExitCode: -1}, err: context.DeadlineExceeded},
		"truncated":    {result: system.CommandResult{Stdout: `{"pkg_count":0}`, StdoutTruncated: true}},
	} {
		if _, err := auditAdapter(r).Audit(context.Background(), []vulns.Package{{Name: "a", Version: "1"}}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestVulnDatabase(t *testing.T) {
	dir := t.TempDir()
	a := NewAdapter(&auditRunner{})

	a.vulnDatabase = filepath.Join(dir, "vuln.xml")
	if _, _, err := a.Database(context.Background()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing database: %v, want fs.ErrNotExist", err)
	}
	if err := os.WriteFile(a.vulnDatabase, []byte("<vuxml/>"), 0o444); err != nil {
		t.Fatal(err)
	}
	path, updated, err := a.Database(context.Background())
	if err != nil || path != a.vulnDatabase || updated.IsZero() {
		t.Errorf("got %q, %v, %v", path, updated, err)
	}

	a.vulnDatabase = dir
	if _, _, err := a.Database(context.Background()); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a directory: %v, want refused as not a regular file", err)
	}
}

func TestAuditCredentialRefusesRoot(t *testing.T) {
	a := NewAdapter(&auditRunner{})
	a.auditUser = "root"
	if _, err := a.auditCredential(); err == nil {
		t.Fatal("root accepted as the user that reads package databases")
	}
	a.auditUser = "no-such-user-jaildeck"
	if _, err := a.auditCredential(); err == nil {
		t.Fatal("a missing user accepted")
	}
}
