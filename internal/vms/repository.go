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
}

// ErrNoSuchVM is vm-bhyve's "unable to locate virtual machine".
var ErrNoSuchVM = errors.New("no such virtual machine")
