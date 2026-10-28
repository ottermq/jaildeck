package networks

import "context"

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
}
