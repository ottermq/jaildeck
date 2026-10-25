package templates

// Template is a FreeBSD userland a jail can be cloned from.
//
// Nothing here is stored. A template's whole state is derivable from ZFS
// (JD-011): the dataset exists or it does not, its @base snapshot exists or it
// does not, and zfs get origin says what it was derived from. That is why a
// half-built template needs no cleanup and no record -- it is simply a
// template that is not ready, and it says so.
type Template struct {
	// Name is the last component of the dataset, which is what a client uses:
	// "15.1-RELEASE", "postgres-16".
	Name    string `json:"name"`
	Dataset string `json:"dataset"`
	// Mountpoint is where pkg -c operates and where a jail's clone comes from.
	Mountpoint string `json:"mountpoint"`
	// Ready is whether the @base snapshot exists. A template is usable only
	// then (JD-007), and a derive that failed part way through is a template
	// that exists and is not ready.
	Ready bool `json:"ready"`
	// Parent is the template this one was derived from, empty for a release
	// template built from base.txz. Read from the clone's origin rather than
	// recorded: a derived template cannot outlive or disagree with its parent.
	Parent string `json:"parent,omitempty"`
	// Snapshot is the full dataset@base once it exists, which is what a jail
	// is cloned from.
	Snapshot string `json:"snapshot,omitempty"`
	// State is where this template has got to, read off the filesystem except
	// for the -ing forms, which come from what this process is doing (JD-008).
	// A template that has only been fetched has no dataset and still has a
	// state, because the resource a client watches has to exist from the
	// moment the work starts.
	State string `json:"state"`
	// Running is the step executing right now, absent when nothing is. Held in
	// memory and never written down: a restart kills the work, so a marker
	// that survived it would be claiming something false.
	Running *Running `json:"running,omitempty"`
	Used    int64    `json:"used"`
	// Referenced is the whole userland; for a derived template most of it is
	// still shared with its parent, so Used is what the packages cost.
	Referenced int64 `json:"referenced"`
}

// ReadySnapshot is the snapshot name that marks a template usable (JD-007).
const ReadySnapshot = "base"
