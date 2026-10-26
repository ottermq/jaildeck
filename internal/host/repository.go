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

// VMBhyve is what vm-bhyve says about itself (JD-019). Installed is false when
// `vm` is not on Jail Deck's PATH at all. When it is, Problem is vm-bhyve's own
// ERROR line from `vm list` -- it names each misconfiguration precisely, so
// its words are reported rather than its rules re-derived -- and empty when
// vm-bhyve is configured and answering. Datastore and Dataset are then its
// default datastore's path and ZFS dataset.
type VMBhyve struct {
	Installed bool
	Problem   string
	Datastore string
	Dataset   string
}

type PFStatus struct {
	Loaded  bool
	Enabled bool
}

// Dataset is one line of zfs list. Mountpoint is the property's value, which
// can be "none": a pool's own root often is, and so is every dataset on a
// replication target.
type Dataset struct {
	Name       string
	Mountpoint string
}

// DatasetResult carries the command that ran, so the service can audit what
// actually happened, as jails.OperationResult does.
type DatasetResult struct {
	Dataset string
	Command string
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
	// VMBhyve asks vm-bhyve whether it can run (`vm list`), and where its
	// default datastore is. Read-only: `vm init`, which loads modules and
	// writes the datastore's layout, is rc.d/vm's to run at boot.
	VMBhyve(ctx context.Context) (VMBhyve, error)
	// UEFIFirmware is the path of the firmware bhyve boots a UEFI guest
	// with, empty when edk2-bhyve is not installed.
	UEFIFirmware(ctx context.Context) (string, error)

	// Pools lists the imported pools in the order zpool prints them. A host
	// without ZFS does not lack the binary: zpool exits non-zero.
	Pools(ctx context.Context) ([]string, error)
	// Datasets lists pool and everything beneath it.
	Datasets(ctx context.Context, pool string) ([]Dataset, error)
	// CreateDataset is the only method that writes. props become -o options.
	CreateDataset(ctx context.Context, name string, props map[string]string) (DatasetResult, error)
}
