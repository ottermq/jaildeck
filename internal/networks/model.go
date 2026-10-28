// Package networks is where instances' networks are: for now the one default
// VM network a node gets at setup (#118 step 1, JD-040), later the
// per-tenant networks of #118's step 2 and the read-only view of #128.
package networks

// The default VM network is made of parts, each read off the host on every
// request (JD-011). Some are Jail Deck's to put in place when asked; the rest
// are the operator's, and the answer says exactly what to do about them.
type PartStatus string

const (
	PartPass PartStatus = "pass"
	PartFail PartStatus = "fail"
	// PartUnknown is a part that cannot be judged until another one passes,
	// or that could not be read.
	PartUnknown PartStatus = "unknown"
)

// Who puts a part in place.
const (
	OwnerJailDeck = "jaildeck"
	OwnerOperator = "operator"
)

// The parts, in the order they are put in place.
const (
	PartSwitch           = "switch"
	PartAddress          = "address"
	PartPrivate          = "private"
	PartForwarding       = "forwarding"
	PartForwardingAtBoot = "forwarding_at_boot"
	PartAnchorFile       = "anchor_file"
	PartPF               = "pf"
	PartPFRules          = "pf_rules"
	PartAnchorLoaded     = "anchor_loaded"
)

// Part is one thing the default network needs, and whether the host has it.
type Part struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Status   PartStatus `json:"status"`
	Owner    string     `json:"owner"`
	Observed string     `json:"observed,omitempty"`
	Detail   string     `json:"detail,omitempty"`
	// Remedy is set on a part that fails and is the operator's: what to
	// change, as precisely as it can be said. Jail Deck's own parts are put
	// in place by PUT /api/networks/default instead.
	Remedy string `json:"remedy,omitempty"`
}

// Default is the default VM network as the host has it.
type Default struct {
	// Switch is the vm-bhyve switch it is, and Address the host's address
	// on it, which is the guests' gateway.
	Switch  string `json:"switch"`
	Address string `json:"address"`
	Subnet  string `json:"subnet"`
	// Gateway is Address without its prefix: what a guest is given.
	Gateway string `json:"gateway"`
	// Egress is the interface NAT sends the guests out of.
	Egress string `json:"egress,omitempty"`
	// Usable is whether a guest on the switch reaches the host and no other
	// guest: the switch, its address and its private ports are in place.
	Usable bool `json:"usable"`
	// Ready is whether every part passes, NAT included.
	Ready bool   `json:"ready"`
	Parts []Part `json:"parts"`
}

// Ensured is what PUT /api/networks/default answers: the network afterwards,
// and what this request did to get it there.
type Ensured struct {
	Default
	Changed bool     `json:"changed"`
	Done    []string `json:"done"`
	// Note is said when something done takes effect later: a switch made
	// private applies to guests as they next start.
	Note string `json:"note,omitempty"`
}

// The audit log's names for what putting the network in place runs.
const (
	OperationCreateSwitch           = "create-vm-switch"
	OperationSetSwitchAddress       = "set-vm-switch-address"
	OperationSetSwitchPrivate       = "set-vm-switch-private"
	OperationEnableForwarding       = "enable-ip-forwarding"
	OperationEnableForwardingAtBoot = "enable-ip-forwarding-at-boot"
	OperationWriteAnchor            = "write-pf-anchor"
	OperationLoadAnchor             = "load-pf-anchor"
)

// Anchor is the PF anchor the default network's NAT lives in. The operator's
// pf.conf includes jaildeck/* once; Jail Deck owns what is inside (JD-020's
// phase 3, JD-040).
const Anchor = "jaildeck/vmnet"
