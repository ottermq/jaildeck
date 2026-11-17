// Package vulns audits the packages of the host, every jail and every
// template against the host's vulnerability database (#230, JD-061).
//
// A jail's package database belongs to whoever is root in that jail, so
// what it says is only what that root lets it say: this finds the
// vulnerable packages an honest jail has, and is no detector of a
// compromised one. It is read as nobody, never by the root process, and
// only names and versions shaped like a package's reach pkg audit.
package vulns

import "time"

// Status is what an audit found for one tree.
type Status string

const (
	// Clean is audited against a database that is present, with no
	// vulnerable package. A tree with no packages at all is clean.
	Clean Status = "clean"
	// Vulnerable is audited, with at least one vulnerable package.
	Vulnerable Status = "vulnerable"
	// Unknown is not audited: the packages could not be listed, or there
	// is no database to audit them against. Never read as clean.
	Unknown Status = "unknown"
	// Unchecked is a tree the last audit did not include: one created
	// since, or every tree before the first audit has finished.
	Unchecked Status = "unchecked"
)

// Package is one installed package, its name and version as pkg reports
// them.
type Package struct {
	Name    string
	Version string
}

// String is the name-version pkg audit takes.
func (p Package) String() string { return p.Name + "-" + p.Version }

// Issue is one entry in the vulnerability database that a package matches.
type Issue struct {
	Description string `json:"description"`
	// CVEs may be empty: not every VuXML entry has one.
	CVEs []string `json:"cves"`
	// URL is the entry on vuxml.FreeBSD.org.
	URL string `json:"url"`
	// Affected is the version ranges the entry covers, as VuXML gives
	// them: "< 18.7".
	Affected []string `json:"affected"`
}

// Finding is one vulnerable package and what it is vulnerable to.
type Finding struct {
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Issues  []Issue `json:"issues"`
	// ReverseDependencies is the installed packages that depend on this
	// one. Shown, never counted: they are not vulnerable themselves.
	ReverseDependencies []string `json:"reverse_dependencies"`
}

// Summary is one tree's audit in numbers: what a jail's or a template's row
// carries.
type Summary struct {
	Status Status `json:"status"`
	// Packages is how many packages are installed, and Vulnerable how many
	// of them the database names. Both are 0 when unknown or unchecked.
	Packages   int `json:"packages"`
	Vulnerable int `json:"vulnerable"`
	// Checked is when this tree was audited; absent when it was not.
	Checked *time.Time `json:"checked,omitempty"`
}

// Report is one tree's audit in full.
type Report struct {
	Summary
	Findings []Finding `json:"findings"`
	// Reason says why the status is unknown or unchecked.
	Reason string `json:"reason,omitempty"`
}

// Named is a jail's or a template's report, by name.
type Named struct {
	Name string `json:"name"`
	Report
}

// Database is the vulnerability database every audit used: the host's,
// which the daily security run keeps current. Jail Deck never fetches it.
type Database struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
	// Updated is the file's modification time, when it is present.
	Updated *time.Time `json:"updated,omitempty"`
	// Stale is a database older than StaleAfter: an old database finds
	// nothing, and that looks like good news.
	Stale bool `json:"stale"`
}

// StaleAfter is how old the database may be before it is called stale. The
// daily security run fetches it every day, so three days is two missed runs.
const StaleAfter = 72 * time.Hour

// Snapshot is the last audit of everything.
type Snapshot struct {
	// Checked is when the last audit finished; absent until the first one
	// has.
	Checked   *time.Time `json:"checked,omitempty"`
	Database  Database   `json:"database"`
	Host      Report     `json:"host"`
	Jails     []Named    `json:"jails"`
	Templates []Named    `json:"templates"`
}

// Target is a tree to audit: a jail's path or a template's mountpoint.
type Target struct {
	Name string
	Root string
}
