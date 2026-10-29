package networks

import (
	"context"
	"net/netip"
)

// Switch is one row of `vm switch list`.
type Switch struct {
	Name string
	Type string
	// Bridge is the switch's interface on the host, vm-<name> for a
	// standard switch.
	Bridge string
	// Address is the host's address on the switch, empty when it has none.
	Address string
	Private bool
}

// PF is what Jail Deck can tell of PF without changing it.
type PF struct {
	Loaded  bool
	Enabled bool
	// NatAnchor is whether the loaded main ruleset evaluates jaildeck/*'s
	// NAT rules: `nat-anchor "jaildeck/*"`, as pfctl -s nat prints it.
	NatAnchor bool
	// RulesFile is the file rc.d/pf loads (pf_rules), and RulesText its
	// contents, empty when it could not be read: what says whether the
	// anchor survives a reboot.
	RulesFile string
	RulesText string
}

// System is what the default network is read from and put in place with. The
// writes return the command they ran, for the audit entry.
type System interface {
	// Switches is `vm switch list`. vm-bhyve not configured is an error
	// that says so.
	Switches(ctx context.Context) ([]Switch, error)
	Sysctl(ctx context.Context, name string) (string, error)
	// RCConf is `sysrc -n <name>`, which answers rc.conf's value or the
	// default from /etc/defaults/rc.conf.
	RCConf(ctx context.Context, name string) (string, error)
	// DefaultRouteInterface is the interface the default route leaves by,
	// empty when there is no default route.
	DefaultRouteInterface(ctx context.Context) (string, error)
	PF(ctx context.Context) (PF, error)
	// AnchorRules is the NAT rules loaded in anchor, as pfctl prints them.
	AnchorRules(ctx context.Context, anchor string) ([]string, error)
	// AnchorFile is the file's contents, empty when it does not exist.
	AnchorFile(ctx context.Context, path string) (string, error)

	CreateSwitch(ctx context.Context, name string) (string, error)
	SetSwitchAddress(ctx context.Context, name, address string) (string, error)
	SetSwitchPrivate(ctx context.Context, name string) (string, error)
	SetSysctl(ctx context.Context, name, value string) (string, error)
	SetRCConf(ctx context.Context, name, value string) (string, error)
	WriteAnchorFile(ctx context.Context, path, content string) (string, error)
	// LoadAnchor validates the file with `pfctl -n` and only then loads it
	// into anchor, replacing what was there. A file pfctl refuses leaves the
	// loaded rules as they were (measured).
	LoadAnchor(ctx context.Context, anchor, path string) (string, error)

	// Tenant networks (JD-043). TenantBridges lists the bridges named jdn<i>,
	// each with its description, address and members; AddressesInUse is every
	// IPv4 prefix configured on any interface of the host.
	TenantBridges(ctx context.Context) ([]TenantBridge, error)
	AddressesInUse(ctx context.Context) ([]netip.Prefix, error)
	// CreateTenantBridge is `ifconfig bridge create name <bridge> description
	// <d> up`, which is not atomic: measured, it can fail with the bridge
	// already made, so the service destroys it again on any failure.
	CreateTenantBridge(ctx context.Context, bridge, description string) (string, error)
	SetBridgeAddress(ctx context.Context, bridge, address string) (string, error)
	DestroyBridge(ctx context.Context, bridge string) (string, error)
	// CreateManualSwitch is a vm-bhyve switch on a bridge Jail Deck made.
	// DestroySwitch also removes the bridge_<name> line vm switch destroy
	// leaves in system.conf for a manual switch (measured).
	CreateManualSwitch(ctx context.Context, name, bridge string) (string, error)
	DestroySwitch(ctx context.Context, name string) (string, error)
	// SwitchUsers are the guests whose configuration puts a NIC on the
	// switch, running or not; BridgeUsers the jails whose configuration
	// names the bridge.
	SwitchUsers(ctx context.Context, name string) ([]string, error)
	BridgeUsers(ctx context.Context, bridge string) ([]string, error)
}

// TenantBridge is one jdn<i> bridge as the host has it.
type TenantBridge struct {
	Name        string
	Description string
	// Address is the host's address on it, the network's gateway; zero when
	// it has none.
	Address netip.Prefix
	Members []string
}
