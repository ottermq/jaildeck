package vms

import (
	"context"
	"errors"
)

// VMSystem reads vm-bhyve (JD-035) and starts and stops its guests
// (JD-036). It cannot create, change or destroy one: those are methods still
// to be added, each with its reason.
//
// The three lifecycle methods return the command they ran, for the audit
// entry, and vm-bhyve's own words in the error when it refused.
type VMSystem interface {
	// VMList is `vm list`. vm-bhyve not installed, or refusing to run until
	// it is configured, is a Listing that says so, not an error.
	VMList(ctx context.Context) (Listing, error)
	// VMInfo is `vm info <name>`: ErrNoSuchVM when vm-bhyve has no guest by
	// that name.
	VMInfo(ctx context.Context, name string) (Info, error)

	// VMStart is `vm start <name>`, detached from Jail Deck: it returns once
	// vm-bhyve has put its supervisor in the background, before the guest
	// is running.
	VMStart(ctx context.Context, name string) (string, error)
	// VMStop is `vm stop <name>`: the ACPI power button. It returns before
	// the guest has stopped, and a guest that ignores the button never does.
	VMStop(ctx context.Context, name string) (string, error)
	// VMPoweroff is `vm poweroff -f <name>`: the plug pulled.
	VMPoweroff(ctx context.Context, name string) (string, error)

	// VMSwitches lists vm-bhyve's virtual switches by name. vm-bhyve itself
	// accepts a NIC on a switch that does not exist, and the guest then
	// starts with a tap attached to nothing -- measured (JD-038).
	VMSwitches(ctx context.Context) ([]string, error)
	// VMCreate is `vm create` from Jail Deck's own template, with the
	// cloud-init seed the spec describes. The guest has a disk0 of the
	// requested size and no NIC.
	VMCreate(ctx context.Context, spec CreateSpec) (string, error)
	// VMAddNetwork is `vm add -d network -s <switch>`: network0, with the
	// MAC vm create already gave it and the seed matches the NIC by.
	VMAddNetwork(ctx context.Context, name, vmSwitch string) (string, error)
	// VMReplaceDisk swaps the disk0 vm create made for a clone of an image's
	// snapshot, at the guest's size and with volmode=dev (JD-037).
	VMReplaceDisk(ctx context.Context, name, snapshot string, size int64) (string, error)
	// VMDestroy is `vm destroy -f <name>`: the guest's dataset, its disk
	// clone with it, and its configuration.
	VMDestroy(ctx context.Context, name string) (string, error)
}

// ImageCatalog is where a guest's disk comes from: the VM image templates
// (JD-037), defined here so that vms needs nothing of the images package.
type ImageCatalog interface {
	// Image is a ready image's snapshot and disk size. ErrNoSuchImage when
	// there is no image by that name.
	Image(ctx context.Context, name string) (ImageRef, error)
}

// ImageRef is what creating a guest needs to know about its image.
type ImageRef struct {
	Ready    bool
	Snapshot string
	// Size is the image's disk, the smallest a guest's can be.
	Size int64
}

// ErrNoSuchImage is an image name nothing has prepared.
var ErrNoSuchImage = errors.New("no such image")

// ErrNoSuchVM is vm-bhyve's "unable to locate virtual machine".
var ErrNoSuchVM = errors.New("no such virtual machine")
