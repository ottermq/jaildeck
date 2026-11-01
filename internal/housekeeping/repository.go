package housekeeping

import (
	"context"
	"time"
)

// The sources, each another domain's answer, adapted in internal/app so that
// this package imports none of them.

// Template is a template and what depends on it.
type Template struct {
	Name    string
	Dataset string
	Used    int64
	Ready   bool
	// Busy is a template being prepared right now: not unused, unfinished.
	Busy bool
	// Dependents are its clones and holds: a template with any is in use.
	Dependents int
}

type TemplateSource interface {
	Templates(ctx context.Context) ([]Template, error)
}

// Image is a VM image and the guests cloned from it.
type Image struct {
	Name   string
	Used   int64
	Ready  bool
	Busy   bool
	Guests int
}

type ImageSource interface {
	Images(ctx context.Context) ([]Image, error)
}

// Retained is a dataset kept past its jail.
type Retained struct {
	Name    string
	Dataset string
	Used    int64
}

type RetainedSource interface {
	Retained(ctx context.Context) ([]Retained, error)
}

// Snapshot is one snapshot of a jail's own dataset, with what its children's
// snapshots of the same name hold: a destroy takes them together (JD-024).
type Snapshot struct {
	Jail    string
	Name    string // dataset@short
	Short   string
	Used    int64
	Created time.Time
}

type SnapshotSource interface {
	// JailSnapshots lists every jail's own snapshots. A jail whose snapshots
	// cannot be read is named in the error list, not a reason to answer
	// nothing.
	JailSnapshots(ctx context.Context) ([]Snapshot, []error)
}

// Archive is a file Jail Deck downloaded and could download again.
type Archive struct {
	Path     string
	Size     int64
	Modified time.Time
	// For is the release or image source it was fetched for.
	For string
}

type ArchiveSource interface {
	Archives(ctx context.Context) ([]Archive, error)
}
