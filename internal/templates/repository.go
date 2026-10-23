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
	// Derive clones a parent's snapshot into a new template dataset and
	// returns where it is mounted, which is what pkg -c needs.
	Derive(ctx context.Context, parentSnapshot, name string) (string, error)
	// Seal takes the @base snapshot that marks the template usable. Until it
	// runs, the template exists and is not ready -- which is the state a
	// failed derive leaves behind, and it needs no cleanup to be truthful.
	Seal(ctx context.Context, name string) error

	// Dependents names what is holding a template in place: the datasets
	// cloned from any of its snapshots, and the holds on them. Facts, not a
	// judgement -- whether they are a reason to refuse is this package's rule.
	Dependents(ctx context.Context, name string) (Dependents, error)
	// Destroy removes the template and its snapshots. It does not check
	// anything: Dependents is asked first, by the service.
	Destroy(ctx context.Context, name string) error
}

// Dependents is what would be taken, or broken, by destroying a template.
type Dependents struct {
	// Clones are datasets made from one of this template's snapshots -- every
	// jail cloned from it, and every template derived from it.
	Clones []string
	Holds  []string
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
}

// Result is what a package install actually ran, for the audit log.
type Result struct {
	Root    string
	Command string
}
