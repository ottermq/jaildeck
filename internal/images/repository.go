package images

import "context"

// What vm-bhyve can say about itself, the same three answers GET /api/vms
// gives (JD-035).
const (
	VMBhyveReady         = "ready"
	VMBhyveNotInstalled  = "not_installed"
	VMBhyveNotConfigured = "not_configured"
)

// Store is where images are kept: .images under vm-bhyve's default datastore,
// a dataset for the zvols and its directory for the archives (JD-037).
type Store struct {
	VMBhyve string
	// Problem is vm-bhyve's own words when it is not configured, or why its
	// datastore cannot hold images.
	Problem string
	// Dataset is <datastore>/.images. Empty when the datastore is not on ZFS:
	// an image is a zvol, so there is nowhere to put one.
	Dataset string
	// Path is the directory the archives are kept in, the dataset's mount
	// point.
	Path string
}

// Usable is whether images can be prepared in this store.
func (s Store) Usable() bool {
	return s.VMBhyve == VMBhyveReady && s.Dataset != ""
}

// ImageSystem fetches cloud images and keeps them as zvols (JD-037). Each
// method that changes the host returns the command it ran, for the audit
// entry.
type ImageSystem interface {
	// Store finds vm-bhyve's default datastore.
	Store(ctx context.Context) (Store, error)
	// Sources is the catalog, without Fetched filled in.
	Sources() []Source
	// Images lists the zvols under the store's dataset, with their @base and
	// the guests cloned from it. None at all, the dataset included, is an
	// empty list.
	Images(ctx context.Context, store Store) ([]Image, error)
	// Fetched lists the sources whose verified archive is in the store.
	Fetched(ctx context.Context, store Store) ([]string, error)
	// Fetch downloads a source's archive and verifies it against the
	// publisher's checksum. Only a verified archive is kept under its name.
	Fetch(ctx context.Context, store Store, source string) (string, error)
	// Write creates the image's zvol, the size of the disk in the archive,
	// and writes the disk into it.
	Write(ctx context.Context, store Store, name, source string) (string, error)
	// Seal takes @base.
	Seal(ctx context.Context, store Store, name string) (string, error)
	// Destroy removes the zvol and its snapshot. The archive stays.
	Destroy(ctx context.Context, store Store, name string) (string, error)
}
