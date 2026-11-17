package vulns

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
)

// Source lists a tree's packages and audits a list of them. The FreeBSD
// adapter does both as nobody (JD-061).
type Source interface {
	// Installed lists the packages installed in the tree at root, and how
	// many lines were skipped for not being shaped like a package.
	Installed(ctx context.Context, root string) (pkgs []Package, skipped int, err error)
	// Audit checks packages against the database. It is never called
	// with none: pkg audit given no names audits the host instead.
	Audit(ctx context.Context, pkgs []Package) ([]Finding, error)
	// Database is the database's path and when it was last written. An
	// error wrapping fs.ErrNotExist is a database that is not there.
	Database(ctx context.Context) (path string, updated time.Time, err error)
}

// Targets is the jails and templates to audit, from their own services.
type Targets interface {
	Jails(ctx context.Context) ([]Target, error)
	Templates(ctx context.Context) ([]Target, error)
}

// Poll is how often Run asks whether to audit again, which costs a stat of
// the database; Every is how old an audit may get before the next. A new
// database is audited against within Poll of being fetched.
const (
	Poll  = 5 * time.Minute
	Every = time.Hour
)

// Service keeps the last audit and runs the next one. Nothing a request
// does runs an audit: it takes seconds on a host with a few dozen jails, and
// a vulnerability database changes once a day.
type Service struct {
	source   Source
	targets  Targets
	hostRoot string
	now      func() time.Time

	// auditing keeps two audits from running at once.
	auditing sync.Mutex

	mu   sync.RWMutex
	snap Snapshot
	// dbSeen is the database's modification time at the last audit, so a
	// new database is audited against without waiting for the hour.
	dbSeen time.Time
}

// NewService starts with nothing audited: every tree is unchecked until the
// first audit has finished.
func NewService(source Source, targets Targets) *Service {
	return &Service{
		source: source, targets: targets, hostRoot: "/", now: time.Now,
		snap: Snapshot{
			Host:  Report{Summary: Summary{Status: Unchecked}, Findings: []Finding{}, Reason: notYet},
			Jails: []Named{}, Templates: []Named{},
		},
	}
}

const notYet = "the first audit has not finished"

// Snapshot is the last audit. Nothing in it is changed after it is
// published, so it is safe to read without a copy.
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Jail is one jail's audit in numbers; unchecked when the last audit did
// not include it.
func (s *Service) Jail(name string) Summary {
	return s.find(name, func(sn Snapshot) []Named { return sn.Jails })
}

// Template is one template's audit in numbers.
func (s *Service) Template(name string) Summary {
	return s.find(name, func(sn Snapshot) []Named { return sn.Templates })
}

func (s *Service) find(name string, list func(Snapshot) []Named) Summary {
	snap := s.Snapshot()
	for _, n := range list(snap) {
		if n.Name == name {
			return n.Summary
		}
	}
	return Summary{Status: Unchecked}
}

// Run audits once now, then checks every poll whether to audit again: when
// the last audit is older than every, or the database has changed since.
// It returns when ctx is done.
func (s *Service) Run(ctx context.Context, poll, every time.Duration) {
	s.Audit(ctx)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.due(ctx, every) {
				s.Audit(ctx)
			}
		}
	}
}

func (s *Service) due(ctx context.Context, every time.Duration) bool {
	s.mu.RLock()
	checked, seen := s.snap.Checked, s.dbSeen
	s.mu.RUnlock()
	if checked == nil || s.now().Sub(*checked) >= every {
		return true
	}
	_, updated, err := s.source.Database(ctx)
	if err != nil {
		// Gone since the last audit: audit again, to say so.
		return !seen.IsZero()
	}
	return !updated.Equal(seen)
}

// Audit audits the host, every jail and every template, and publishes the
// result. A tree that cannot be audited is unknown, and never fails the
// others.
func (s *Service) Audit(ctx context.Context) Snapshot {
	s.auditing.Lock()
	defer s.auditing.Unlock()
	log := common.Logger(ctx, "vulns")

	snap := Snapshot{Jails: []Named{}, Templates: []Named{}}
	path, updated, err := s.source.Database(ctx)
	snap.Database.Path = path
	missing := ""
	switch {
	case errors.Is(err, fs.ErrNotExist):
		missing = fmt.Sprintf("there is no vulnerability database at %s; the daily security run fetches it when security_status_pkgaudit_enable is on", path)
	case err != nil:
		missing = bounded("the vulnerability database could not be read: " + err.Error())
	default:
		snap.Database.Present = true
		snap.Database.Updated = &updated
		snap.Database.Stale = s.now().Sub(updated) > StaleAfter
	}
	if missing != "" {
		log.Warn("auditing nothing", slog.String("reason", missing))
	}

	tree := func(root string) Report {
		if missing != "" {
			return unknown(missing)
		}
		return s.tree(ctx, log, root)
	}
	snap.Host = tree(s.hostRoot)
	for _, group := range []struct {
		what string
		list func(context.Context) ([]Target, error)
		into *[]Named
	}{{"jails", s.targets.Jails, &snap.Jails}, {"templates", s.targets.Templates, &snap.Templates}} {
		targets, err := group.list(ctx)
		if err != nil {
			// Left out, so each reads as unchecked rather than clean.
			log.Warn("auditing without the "+group.what, slog.Any("err", err))
			continue
		}
		for _, t := range targets {
			*group.into = append(*group.into, Named{Name: t.Name, Report: tree(t.Root)})
		}
	}

	finished := s.now()
	snap.Checked = &finished
	stamp := func(r *Report) {
		if r.Status == Clean || r.Status == Vulnerable {
			r.Checked = &finished
		}
	}
	stamp(&snap.Host)
	for i := range snap.Jails {
		stamp(&snap.Jails[i].Report)
	}
	for i := range snap.Templates {
		stamp(&snap.Templates[i].Report)
	}

	s.mu.Lock()
	s.snap = snap
	s.dbSeen = time.Time{}
	if snap.Database.Updated != nil {
		s.dbSeen = *snap.Database.Updated
	}
	s.mu.Unlock()
	return snap
}

// tree audits the packages installed under root.
func (s *Service) tree(ctx context.Context, log *slog.Logger, root string) Report {
	if root == "" {
		return unknown("it has no path to read its packages from")
	}
	pkgs, skipped, err := s.source.Installed(ctx, root)
	if err != nil {
		return unknown(bounded("its packages could not be listed: " + err.Error()))
	}
	if skipped > 0 {
		log.Warn("skipped package lines not shaped like a package", slog.String("root", root), slog.Int("lines", skipped))
	}
	if len(pkgs) == 0 {
		// Never audited: pkg audit given no names audits the host.
		return Report{Summary: Summary{Status: Clean}, Findings: []Finding{}}
	}
	findings, err := s.source.Audit(ctx, pkgs)
	if err != nil {
		return unknown(bounded("its packages could not be audited: " + err.Error()))
	}
	status := Clean
	if len(findings) > 0 {
		status = Vulnerable
	}
	return Report{Summary: Summary{Status: status, Packages: len(pkgs), Vulnerable: len(findings)}, Findings: findings}
}

func unknown(reason string) Report {
	return Report{Summary: Summary{Status: Unknown}, Findings: []Finding{}, Reason: reason}
}

// bounded keeps a reason readable when it carries pkg's own message, which
// for a jail's tree may say whatever that jail made it say.
func bounded(s string) string {
	const limit = 300
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
