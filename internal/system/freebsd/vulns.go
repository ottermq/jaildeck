package freebsd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vulns"
)

// defaultVulnDatabase is where FreeBSD's daily security run keeps the
// vulnerability database: periodic's security_status_pkgaudit, which runs
// `pkg audit -F` and is on by default. Jail Deck reads it and never fetches
// it (#230).
const defaultVulnDatabase = "/var/db/pkg/vuln.xml"

const (
	// listTimeout and listOutput bound one tree's package list: measured,
	// 0.01 s for a jail of twenty packages; a host has a few hundred.
	listTimeout = 30 * time.Second
	listOutput  = 4 << 20
	// auditTimeout and auditOutput bound one tree's audit: measured, 0.2 s
	// and 4.6 KB for five vulnerable packages. A package with fifty CVEs is
	// a few kilobytes more.
	auditTimeout = 2 * time.Minute
	auditOutput  = 32 << 20
)

// errNoPackages is an audit asked for with no packages. pkg audit given no
// names audits the host's own packages, which would be reported as the
// jail's.
var errNoPackages = errors.New("no packages to audit")

// Installed lists the packages installed in the tree at root -- a jail's
// path, a template's mountpoint, or / for the host -- with
// `pkg -r <root> query` (#230, JD-061).
//
// The database belongs to whoever is root in that jail, so it is opened by
// nobody and never by Jail Deck: a file crafted against pkg or SQLite
// reaches an unprivileged process. Run as root, pkg -r also writes SQLite's
// -shm and -wal files into the tree, a template included; measured, as
// nobody it writes nothing. -r rather than -j, because -j runs pkg as root
// inside the jail and works only on a running one, while -r reads a stopped
// jail and a template the same way.
func (a *Adapter) Installed(ctx context.Context, root string) ([]vulns.Package, int, error) {
	if !filepath.IsAbs(root) {
		return nil, 0, fmt.Errorf("%q is not an absolute path", root)
	}
	cred, err := a.auditCredential()
	if err != nil {
		return nil, 0, err
	}
	result, err := a.runHost(ctx, system.Command{
		Name: "pkg", Args: []string{"-r", root, "query", "%n\t%v"},
		Timeout: listTimeout, MaxOutput: listOutput, Credential: cred,
	})
	if err != nil {
		return nil, 0, err
	}
	if result.StdoutTruncated {
		return nil, 0, fmt.Errorf("the package list of %s is over %d bytes", root, listOutput)
	}
	// A tree with no package database says "No packages installed" on
	// stderr and exits 0: a list of none.
	return vulns.ParseInstalled([]byte(result.Stdout))
}

// Audit checks packages against the host's database with
// `pkg audit --raw=json-compact -f <database> -- <name-version>...`, as
// nobody, each package its own argument and never through a shell.
//
// pkg audit exits 1 when it finds something, and also when the database is
// missing, so the exit status decides nothing on its own: the output does,
// and output that is not an audit is an error, never "nothing found".
func (a *Adapter) Audit(ctx context.Context, pkgs []vulns.Package) ([]vulns.Finding, error) {
	if len(pkgs) == 0 {
		return nil, errNoPackages
	}
	args := []string{"audit", "--raw=json-compact", "-f", a.vulnDatabase, "--"}
	for _, p := range pkgs {
		if !p.Valid() {
			return nil, fmt.Errorf("%q is not shaped like a package", p.String())
		}
		args = append(args, p.String())
	}
	cred, err := a.auditCredential()
	if err != nil {
		return nil, err
	}
	cmd := system.Command{Name: "pkg", Args: args, Timeout: auditTimeout, MaxOutput: auditOutput, Credential: cred}
	result, err := a.runner.Run(ctx, cmd)
	if err != nil && result.ExitCode != 1 {
		if msg := strings.TrimSpace(result.Stderr); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return nil, &system.CommandError{Command: cmd.Name, Args: cmd.Args[:5], Result: result, Err: err}
	}
	if result.StdoutTruncated {
		return nil, fmt.Errorf("pkg audit's output is over %d bytes", auditOutput)
	}
	findings, parseErr := vulns.ParseAudit([]byte(result.Stdout))
	if parseErr != nil {
		if msg := strings.TrimSpace(result.Stderr); msg != "" {
			return nil, fmt.Errorf("%w: %s", parseErr, msg)
		}
		return nil, parseErr
	}
	return findings, nil
}

// Database is the host's vulnerability database and when it was last
// written. It must be a regular file; a missing one wraps fs.ErrNotExist.
func (a *Adapter) Database(context.Context) (string, time.Time, error) {
	info, err := os.Stat(a.vulnDatabase)
	if err != nil {
		return a.vulnDatabase, time.Time{}, err
	}
	if !info.Mode().IsRegular() {
		return a.vulnDatabase, time.Time{}, fmt.Errorf("%s is not a regular file", a.vulnDatabase)
	}
	return a.vulnDatabase, info.ModTime(), nil
}

// auditCredential is nobody's, looked up on each use rather than once at
// startup, so a host without the account says so on the page rather than
// refusing to start.
func (a *Adapter) auditCredential() (*syscall.Credential, error) {
	u, err := user.Lookup(a.auditUser)
	if err != nil {
		return nil, fmt.Errorf("looking up the user that reads package databases: %w", err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("user %s has uid %q: %w", a.auditUser, u.Uid, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("user %s has gid %q: %w", a.auditUser, u.Gid, err)
	}
	if uid == 0 || gid == 0 {
		return nil, fmt.Errorf("user %s is root's uid or gid, which defeats reading as it", a.auditUser)
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}, nil
}

var _ vulns.Source = (*Adapter)(nil)
