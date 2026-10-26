package vms

import (
	"context"
	"errors"
)

// VMSystem reads vm-bhyve. Nothing more: there is no method here that could
// start, stop or change a guest, and the adapter is tested for never running
// a vm-bhyve command that would (JD-035). Lifecycle is its own unit, with its
// own methods -- not a flag added to one of these.
type VMSystem interface {
	// VMList is `vm list`. vm-bhyve not installed, or refusing to run until
	// it is configured, is a Listing that says so, not an error.
	VMList(ctx context.Context) (Listing, error)
	// VMInfo is `vm info <name>`: ErrNoSuchVM when vm-bhyve has no guest by
	// that name.
	VMInfo(ctx context.Context, name string) (Info, error)
}

// ErrNoSuchVM is vm-bhyve's "unable to locate virtual machine".
var ErrNoSuchVM = errors.New("no such virtual machine")
