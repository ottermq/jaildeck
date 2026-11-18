package templates

import (
	"github.com/ottermq/jaildeck/internal/osversion"
	"github.com/ottermq/jaildeck/internal/vulns"
)

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
	// Userland is the template's FreeBSD userland and how it stands against
	// the host's (#229). Every jail cloned from it starts at this patch
	// level. Absent until there is a tree to read it from.
	Userland *osversion.Standing `json:"userland,omitempty"`
	// Vulnerabilities is the template's last package audit in numbers
	// (#230, JD-061). Every jail cloned from it starts with these packages.
	// Absent until there is a tree to audit.
	Vulnerabilities *vulns.Summary `json:"vulnerabilities,omitempty"`
	// Built is how Jail Deck made this template, from its jaildeck:built
	// user property (#252): release, derived, refreshed or jail. Empty for a
	// template made before the property existed, or by hand. A refresh runs
	// freebsd-update and pkg as root on the tree, so it is allowed only on a
	// tree Jail Deck built itself, never one a jail's root filled (JD-061).
	Built string `json:"built,omitempty" enums:"release,derived,refreshed,jail"`
}

// BuiltProperty is the ZFS user property that records how a template was made
// (#252). Kept in ZFS, as everything about a template is (JD-011).
const BuiltProperty = "jaildeck:built"

// RefreshedFromProperty is the template a refreshed one was copied from
// (#258). A copy has no origin to read its parent from.
const RefreshedFromProperty = "jaildeck:refreshed-from"

// How a template was made.
const (
	// BuiltRelease is extracted from a release's base.txz.
	BuiltRelease = "release"
	// BuiltDerived is a clone of another template plus packages, by pkg -c.
	BuiltDerived = "derived"
	// BuiltRefreshed is a clone of another template, brought to its
	// release's current patch level and its packages upgraded.
	BuiltRefreshed = "refreshed"
	// BuiltJail is promoted from a jail: whatever that jail's root left in
	// it. Never refreshed.
	BuiltJail = "jail"
)

// Refreshable is whether a refresh may run freebsd-update and pkg as root on
// a clone of this template: only when Jail Deck built the tree itself.
func (t Template) Refreshable() bool {
	switch t.Built {
	case BuiltRelease, BuiltDerived, BuiltRefreshed:
		return true
	}
	return false
}

// ReadySnapshot is the snapshot name that marks a template usable (JD-007).
const ReadySnapshot = "base"
