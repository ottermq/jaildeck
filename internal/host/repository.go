package host

import (
	"context"
	"errors"
)

// ErrUnknownOID is returned by Sysctl when the kernel has no such oid. That is
// a fact about the kernel, not a failure to ask: kern.racct.enable is missing
// from a kernel built without RACCT, where no tunable could enable it.
var ErrUnknownOID = errors.New("unknown sysctl oid")

// DevfsRuleset is a ruleset loaded in the kernel. Number is the fact; Name is
// a courtesy taken from the devfs.rules header that declares it, and is empty
// when no header names that number.
type DevfsRuleset struct {
	Name   string
	Number int
}

// CPUVirtualization is what the boot messages say about the CPU's hardware
// virtualisation. Reported is false when they carry no such line at all, which
// is not evidence the CPU lacks it: the boot log may simply be gone.
type CPUVirtualization struct {
	Reported       bool
	DisabledInBIOS bool
	// Features are the line's tokens, in the order the kernel printed them.
	Features []string
}

type PFStatus struct {
	Loaded  bool
	Enabled bool
}

// HostSystem observes the host. Every method returns what the host said and
// nothing more: pass, fail and the remedies are the service's, so the rules
// test on any OS without a FreeBSD host or a faked command.
type HostSystem interface {
	Sysctl(ctx context.Context, name string) (string, error)
	// JailConfDefaults returns the settings at the top level of /etc/jail.conf.
	// The map is nil when the file does not exist, and non-nil (possibly
	// empty) when it does: a host can run jails with no jail.conf at all.
	JailConfDefaults(ctx context.Context) (map[string]string, error)
	Bridges(ctx context.Context) ([]string, error)
	DevfsRulesets(ctx context.Context) ([]DevfsRuleset, error)
	CPUVirtualization(ctx context.Context) (CPUVirtualization, error)
	PFStatus(ctx context.Context) (PFStatus, error)
}
