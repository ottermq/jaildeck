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
}

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
