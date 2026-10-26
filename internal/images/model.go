package images

// Image is a VM image template: a cloud image's disk in a zvol, sealed with an
// @base snapshot that guests' disks are cloned from (JD-037). It is to a VM
// what a release template is to a jail.
//
// Nothing here is stored. The zvol exists or it does not, @base exists or it
// does not, and where it came from is on the zvol itself, as user properties
// set when it was created (JD-011).
type Image struct {
	// Name is the zvol's last component, which is what a client uses.
	Name string `json:"name"`
	// Source is the catalog entry the image was written from. Empty for a
	// zvol under .images that Jail Deck did not write.
	Source  string `json:"source,omitempty"`
	Dataset string `json:"dataset,omitempty"`
	// Ready is whether @base exists. An image is usable only then, and one
	// whose write stopped part way is a zvol that is not ready.
	Ready    bool   `json:"ready"`
	Snapshot string `json:"snapshot,omitempty"`
	// State is read off the disk except for the -ing forms, which come from
	// what this process is doing (JD-008).
	State   string   `json:"state"`
	Running *Running `json:"running,omitempty"`
	// Size is the zvol's volsize: the image's disk, and the smallest disk a
	// guest cloned from it can have.
	Size int64 `json:"size,omitempty"`
	// Used is what the image costs the pool. Sparse and compressed, so far
	// less than Size.
	Used int64 `json:"used,omitempty"`
	// SHA512 is the checksum of the archive the disk was written from. The
	// source's "latest" moves; this names the exact build.
	SHA512 string `json:"sha512,omitempty"`
	// Guests are the guests whose disk is a clone of @base. An image is not
	// removed while there are any.
	Guests []string `json:"guests"`
}

// Source is an image the adapter knows how to fetch, verify and unpack
// (JD-037). A catalog rather than a URL, because cloud images do not share a
// format: each entry is one the adapter actually handles.
type Source struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// URL is the archive's, for the operator to see what will be fetched.
	URL string `json:"url"`
	// Fetched is whether the verified archive is already on the host, so
	// preparing an image from it starts at the write.
	Fetched bool `json:"fetched"`
}

// Listing is the whole of what GET /api/vm-images answers.
type Listing struct {
	// VMBhyve is the same answer GET /api/vms gives: ready, not_installed or
	// not_configured. Images live in vm-bhyve's datastore.
	VMBhyve string `json:"vm_bhyve"`
	// Problem is why images cannot be prepared here, when they cannot.
	Problem string `json:"problem,omitempty"`
	// Dataset is where images are kept: .images under vm-bhyve's default
	// datastore.
	Dataset string   `json:"dataset,omitempty"`
	Images  []Image  `json:"images"`
	Sources []Source `json:"sources"`
}

// Removal is what DELETE /api/vm-images/{name} answers: what was destroyed.
type Removal struct {
	Name    string `json:"name"`
	Dataset string `json:"dataset"`
}

// ReadySnapshot is the snapshot that makes an image usable, as it does a
// template (JD-007).
const ReadySnapshot = "base"

// An image's states (JD-008, JD-037).
const (
	// StateFetching and StateWriting come from the in-memory record of what
	// this process is doing, not from the disk.
	StateFetching = "fetching"
	StateWriting  = "writing"
	// StateWritten is a zvol with no @base: a write that stopped, or one
	// whose seal did. The disk cannot tell half an image from a whole one,
	// so nothing is built on it.
	StateWritten = "written"
	StateReady   = "ready"
)

// The audit log's names for the stages.
const (
	OperationFetch   = "fetch-image"
	OperationWrite   = "write-image"
	OperationSeal    = "seal-image"
	OperationDestroy = "destroy-image"
)
