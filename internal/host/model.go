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
