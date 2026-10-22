package host

// CheckStatus is a check's answer. Unknown is a third answer, not a softer
// fail: a check that could not observe the host says so, because a wrong fail
// sends the operator to fix something that may well be fine.
type CheckStatus string

const (
	CheckPass    CheckStatus = "pass"
	CheckFail    CheckStatus = "fail"
	CheckUnknown CheckStatus = "unknown"
)

type CapabilityStatus string

const (
	CapabilityReady       CapabilityStatus = "ready"
	CapabilityUnavailable CapabilityStatus = "unavailable"
	CapabilityUnknown     CapabilityStatus = "unknown"
)

type CheckID string

const (
	CheckRacct             CheckID = "racct"
	CheckExecTimeout       CheckID = "exec_timeout"
	CheckBridges           CheckID = "bridges"
	CheckDevfsRulesets     CheckID = "devfs_rulesets"
	CheckCPUVirtualization CheckID = "cpu_virtualization"
	CheckPF                CheckID = "pf"
	CheckZFSPool           CheckID = "zfs_pool"
	CheckZFSLayout         CheckID = "zfs_layout"
)

type CapabilityID string

const (
	CapabilityJails        CapabilityID = "jails"
	CapabilityLimits       CapabilityID = "limits"
	CapabilityVNET         CapabilityID = "vnet"
	CapabilityProvisioning CapabilityID = "provisioning"
	CapabilityVMs          CapabilityID = "vms"
	CapabilityFirewall     CapabilityID = "firewall"
)

// Check is one observation of the host and the judgment made on it.
type Check struct {
	ID     CheckID     `json:"id"`
	Title  string      `json:"title"`
	Status CheckStatus `json:"status"`
	// Observed is what the host actually said, empty when nothing could be
	// observed.
	Observed string `json:"observed"`
	Detail   string `json:"detail"`
	// Remedy is set on every failed check and only there: a command for a
	// root shell where one exists, otherwise the file or setting to change.
	Remedy string         `json:"remedy"`
	Gates  []CapabilityID `json:"gates"`
}

type Capability struct {
	ID      CapabilityID     `json:"id"`
	Title   string           `json:"title"`
	Status  CapabilityStatus `json:"status"`
	GatedBy []CheckID        `json:"gated_by"`
}

// Report is derived state, recomputed on every request (JD-011).
type Report struct {
	Capabilities []Capability `json:"capabilities"`
	Checks       []Check      `json:"checks"`
}

type checkSpec struct {
	id    CheckID
	title string
	gates []CapabilityID
}

type capabilitySpec struct {
	id    CapabilityID
	title string
}

// checkSpecs and capabilitySpecs fix both the vocabulary and the order of the
// report: clients render the arrays as they arrive.
var checkSpecs = []checkSpec{
	{CheckRacct, "kern.racct.enable", []CapabilityID{CapabilityLimits}},
	{CheckExecTimeout, "exec.timeout in jail.conf defaults", []CapabilityID{CapabilityJails}},
	{CheckBridges, "Network bridges", []CapabilityID{CapabilityVNET}},
	{CheckDevfsRulesets, "devfs rulesets", []CapabilityID{CapabilityProvisioning}},
	{CheckZFSPool, "ZFS pool", []CapabilityID{CapabilityProvisioning}},
	{CheckZFSLayout, "Jails dataset layout", []CapabilityID{CapabilityProvisioning}},
	{CheckCPUVirtualization, "CPU virtualization (VT-x, EPT, UG)", []CapabilityID{CapabilityVMs}},
	{CheckPF, "PF", []CapabilityID{CapabilityFirewall}},
}

var capabilitySpecs = []capabilitySpec{
	{CapabilityJails, "Jail lifecycle"},
	{CapabilityLimits, "Resource limits (rctl)"},
	{CapabilityVNET, "VNET networking"},
	{CapabilityProvisioning, "Jail provisioning"},
	{CapabilityVMs, "Virtual machines (bhyve)"},
	{CapabilityFirewall, "Firewall visibility (PF)"},
}

// OperationCreateDataset names the audit entry for each zfs create. Lowercase
// is load-bearing: the audit filter lowercases the query value but compares
// exactly, so a mixed-case name could never be found.
const OperationCreateDataset = "create-dataset"

// jailsMountpoint is the handbook path, fixed until a host needs otherwise.
const jailsMountpoint = "/usr/local/jails"

// layoutChildren sit beneath <pool>/jails and inherit its mountpoint, in the
// order they are reported and created.
var layoutChildren = []string{"media", "templates", "containers"}

// LayoutDataset is one dataset of the jails layout, present or not.
type LayoutDataset struct {
	Name               string `json:"name"`
	Present            bool   `json:"present"`
	Mountpoint         string `json:"mountpoint"`
	ExpectedMountpoint string `json:"expected_mountpoint"`
	// MountpointTakenBy is set on a missing dataset when another dataset, on
	// any pool, already has its expected mountpoint. Ensure refuses then.
	MountpointTakenBy string `json:"mountpoint_taken_by,omitempty"`
}

// Misplaced reports a dataset that exists but is mounted somewhere else.
func (d LayoutDataset) Misplaced() bool {
	return d.Present && d.Mountpoint != d.ExpectedMountpoint
}

// Layout is what exists of <pool>/jails and its children. It is reported per
// dataset because a layout is routinely partly there.
type Layout struct {
	Pool     string          `json:"pool"`
	Datasets []LayoutDataset `json:"datasets"`
	Missing  []string        `json:"missing"`
}

func (l Layout) misplaced() []LayoutDataset {
	misplaced := make([]LayoutDataset, 0)
	for _, d := range l.Datasets {
		if d.Misplaced() {
			misplaced = append(misplaced, d)
		}
	}
	return misplaced
}

func (l Layout) taken() []LayoutDataset {
	taken := make([]LayoutDataset, 0)
	for _, d := range l.Datasets {
		if !d.Present && d.MountpointTakenBy != "" {
			taken = append(taken, d)
		}
	}
	return taken
}

// LayoutChange is the outcome of ensuring the layout: the layout as it now
// stands, and what this request created to get it there.
type LayoutChange struct {
	Layout
	Changed bool     `json:"changed"`
	Created []string `json:"created"`
}
