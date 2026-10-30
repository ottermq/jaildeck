// Package housekeeping finds what storage is tied up in things nothing uses
// (#125, JD-049): templates and images nothing is cloned from, retained
// datasets, old snapshots, downloaded archives. It reads, and says how each
// one is removed; removing is the existing endpoints' work, with their own
// refusals, so nothing here can take something another thing depends on.
package housekeeping

import "time"

// The kinds of thing that can be reclaimed.
const (
	KindTemplate = "template"
	KindImage    = "image"
	KindRetained = "retained"
	KindSnapshot = "snapshot"
	KindArchive  = "archive"
)

// Item is one thing that could be reclaimed.
type Item struct {
	Kind string `json:"kind"`
	// Name is what the removal takes: a template's or an image's name, a
	// retained dataset's short name, a snapshot's dataset@snapshot, an
	// archive's path.
	Name string `json:"name"`
	// Jail is whose a snapshot is.
	Jail string `json:"jail,omitempty"`
	// Size is what removing it would give back, as ZFS or the file system
	// counts it: a dataset's used, a snapshot's used summed over the jail's
	// datasets that share its name, a file's size. Blocks still shared with
	// something else are not counted.
	Size int64 `json:"size"`
	// Why it counts as unused, in words.
	Why     string     `json:"why"`
	Created *time.Time `json:"created,omitempty"`
	// Remove is the request that removes it, confirm included. Absent for
	// what has no endpoint: an archive.
	Remove *Request `json:"remove,omitempty"`
}

// Request is an existing endpoint, as a client sends it.
type Request struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Report is everything reclaimable, read now (JD-011).
type Report struct {
	Items []Item `json:"items"`
	// Total is the sum of every item's size; Kinds is it by kind.
	Total int64            `json:"total"`
	Kinds map[string]int64 `json:"kinds"`
	// SnapshotAge is how old, in days, a snapshot has to be to be listed.
	SnapshotAge int `json:"snapshot_age_days"`
	// SkippedSnapshots counts the old snapshots left out because another
	// tool takes and prunes them (zrepl, sanoid, zfs-auto-snapshot), by the
	// prefix that marks them. Removing one of those breaks that tool.
	SkippedSnapshots map[string]int `json:"skipped_snapshots"`
	// Problems are the sources that could not be read. The rest are still
	// listed: one unreadable kind does not hide the others.
	Problems []string `json:"problems"`
}
