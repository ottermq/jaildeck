package templates

import "context"

// TemplateStorage is the ZFS side, defined here in this domain's own terms and
// implemented by internal/storage (JD-010). Templates are datasets, but a
// template is not a dataset: it has a readiness state and a lineage, and those
// are this package's rules rather than storage's.
type TemplateStorage interface {
	// Templates lists what is under <pool>/jails/templates, with each one's
	// readiness and parent already resolved from ZFS.
	Templates(ctx context.Context) ([]Template, error)
	// Create makes an empty template dataset and returns where it is mounted.
	// A release template starts empty and becomes a userland by having
	// base.txz unpacked into it, which is the one path that clones nothing.
	Create(ctx context.Context, name string) (string, error)
	// Derive clones a parent's snapshot into a new template dataset and
	// returns where it is mounted, which is what pkg -c needs.
	Derive(ctx context.Context, parentSnapshot, name string) (string, error)
	// Seal takes the @base snapshot that marks the template usable. Until it
	// runs, the template exists and is not ready -- which is the state a
	// failed derive leaves behind, and it needs no cleanup to be truthful.
	Seal(ctx context.Context, name string) error
	// Mark records how the template was made, in its jaildeck:built user
	// property (#252). Set before Seal, so a ready template always says.
	Mark(ctx context.Context, name, built string) error
	// Copy makes name an independent copy of parentSnapshot, recording
	// parent as where it came from, and returns where it is mounted (#258).
	Copy(ctx context.Context, parentSnapshot, name, parent string) (string, error)
	// Supersede records that name has been replaced by another template,
	// in its jaildeck:superseded-by user property (#259).
	Supersede(ctx context.Context, name, by string) error
	// RecordOrphans records the packages a refresh left that nothing
	// depends on, in its jaildeck:orphans user property (#264).
	RecordOrphans(ctx context.Context, name string, orphans []string) error

	// Dependents names what is holding a template in place: the datasets
	// cloned from any of its snapshots, and the holds on them. Facts, not a
	// judgement -- whether they are a reason to refuse is this package's rule.
	Dependents(ctx context.Context, name string) (Dependents, error)
	// Destroy removes the template and its snapshots, and records its name
	// as removed (#268). It does not check anything: Dependents is asked
	// first, by the service.
	Destroy(ctx context.Context, name string) error
	// Removed names the templates removed before, oldest first (#268).
	Removed(ctx context.Context) ([]string, error)
}

// Dependents is what would be taken, or broken, by destroying a template.
//
// Tagged for JSON because it is no longer only a refusal: the detail endpoint
// returns it so a client can see what is in the way before it asks, rather
// than discovering it from a 409.
type Dependents struct {
	// Clones are datasets made from one of this template's snapshots -- every
	// jail cloned from it, and every template derived from it.
	Clones []string `json:"clones"`
	Holds  []string `json:"holds"`
}

// Any reports whether anything depends on the template at all.
func (d Dependents) Any() bool { return len(d.Clones) > 0 || len(d.Holds) > 0 }

// PackageSystem installs packages into a template's root.
//
// A host command against a directory, not the exec endpoint: pkg -c chroots,
// so no jail has to be started. The other path -- start a jail on the clone and
// exec into it -- covers everything this cannot, which is anything beyond
// installing packages (JD-027).
type PackageSystem interface {
	Install(ctx context.Context, root string, packages []string) (Result, error)
	// Upgrade upgrades every package installed in root to the repository's
	// current version (#252).
	Upgrade(ctx context.Context, root string) (Result, error)
	// Explicit names the packages installed in root on purpose, the ones
	// pkg autoremove would never take (#264).
	Explicit(ctx context.Context, root string) ([]string, error)
	// MarkExplicit marks one package in root as installed on purpose
	// again, with pkg set -A 0 (#264).
	MarkExplicit(ctx context.Context, root, pkg string) (Result, error)
	// Orphans names the packages in root installed only as a dependency
	// that nothing depends on any more (#264).
	Orphans(ctx context.Context, root string) ([]string, error)
}

// Result is what a package install actually ran, for the audit log.
type Result struct {
	Root    string
	Command string
}
