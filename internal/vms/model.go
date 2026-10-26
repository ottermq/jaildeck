package vms

// What vm-bhyve can say about itself before it lists anything (JD-035). None
// of them is an error: a host without VMs is the common case, and the answer
// to it is a 200 that says so.
const (
	VMBhyveReady         = "ready"
	VMBhyveNotInstalled  = "not_installed"
	VMBhyveNotConfigured = "not_configured"
)

// A guest's state, as `vm list` prints it, lower-cased (JD-035). vm-bhyve has
// these five and no others -- read in its source, lib/vm-core and lib/vm-run.
const (
	// StateRunning is a bhyve process for the guest; PID is that process.
	StateRunning = "running"
	// StateBootloader is bhyveload or grub-bhyve loading the guest's kernel,
	// before bhyve itself runs. PID is the loader.
	StateBootloader = "bootloader"
	StateStopped    = "stopped"
	// StateSuspended is a guest stopped with its state saved to disk.
	StateSuspended = "suspended"
	// StateLocked is a guest another host has marked as running, in a
	// datastore shared between hosts. LockedBy names that host.
	StateLocked = "locked"
)

// Listing is the whole of what GET /api/vms answers.
type Listing struct {
	// VMBhyve is ready, not_installed or not_configured.
	VMBhyve string `json:"vm_bhyve"`
	// Problem is vm-bhyve's own words for why it is not configured --
	// the same words Host Setup reports.
	Problem string `json:"problem,omitempty"`
	VMs     []VM   `json:"vms"`
}

// VM is one row of `vm list`.
//
// CPU and Memory are the guest configuration's own values, as vm-bhyve prints
// them ("2", "512M"), and are not re-parsed: they are what the operator wrote,
// and a unit Jail Deck derived could disagree with it (JD-035).
type VM struct {
	Name      string `json:"name"`
	Datastore string `json:"datastore"`
	Loader    string `json:"loader,omitempty"`
	CPU       string `json:"cpu,omitempty"`
	Memory    string `json:"memory,omitempty"`
	// VNC is the address a running guest's framebuffer listens on, when it
	// has one.
	VNC string `json:"vnc,omitempty"`

	State string `json:"state"`
	PID   *int   `json:"pid,omitempty"`
	// LockedBy is the host named in the guest's run.lock.
	LockedBy string `json:"locked_by,omitempty"`

	// Autostart is whether the guest is in rc.conf's vm_list, which
	// rc.d/vm starts at boot. AutostartOrder is its place in that list,
	// from 1.
	Autostart      bool `json:"autostart"`
	AutostartOrder int  `json:"autostart_order,omitempty"`
}

// Detail is GET /api/vms/{name}: the guest's row, and what `vm info` adds.
type Detail struct {
	VM
	UUID string `json:"uuid,omitempty"`
	// MemoryResident is the guest's resident memory in bytes, from bhyvectl.
	// Only a guest whose bhyve instance exists has one.
	MemoryResident *int64 `json:"memory_resident,omitempty"`
	// Consoles maps a port to its device: "com1": "/dev/nmdm-web.1B". The
	// host side of the null-modem pair is the B end. Running guests only.
	Consoles map[string]string `json:"consoles,omitempty"`
	Networks []Network         `json:"networks"`
	Disks    []Disk            `json:"disks"`
}

// Network is one of the guest's network interfaces.
type Network struct {
	Number    int    `json:"number"`
	Emulation string `json:"emulation,omitempty"`
	Switch    string `json:"switch,omitempty"`
	MAC       string `json:"mac,omitempty"`
	// Device is the tap a running guest is using -- a new one on every
	// start (JD-019) -- and Bridge the bridge it is a member of.
	Device   string `json:"device,omitempty"`
	Bridge   string `json:"bridge,omitempty"`
	BytesIn  *int64 `json:"bytes_in,omitempty"`
	BytesOut *int64 `json:"bytes_out,omitempty"`
}

// Disk is one of the guest's virtual disks.
type Disk struct {
	Number int `json:"number"`
	// DeviceType is vm-bhyve's: file, zvol, sparse-zvol, custom or iscsi.
	DeviceType string `json:"device_type,omitempty"`
	Emulation  string `json:"emulation,omitempty"`
	Path       string `json:"path,omitempty"`
	Size       *int64 `json:"size,omitempty"`
	Used       *int64 `json:"used,omitempty"`
}

// Info is what the adapter reads from `vm info <name>`. The state is the
// listing's, so it is not repeated here.
type Info struct {
	UUID           string
	MemoryResident *int64
	Consoles       map[string]string
	Networks       []Network
	Disks          []Disk
}
