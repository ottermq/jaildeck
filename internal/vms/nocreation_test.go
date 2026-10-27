package vms

import (
	"context"
	"errors"
)

// noCreation is the half of VMSystem the listing and lifecycle tests never
// reach. Embedded in their stubs, it fails loudly if they do.
type noCreation struct{}

var errNotScripted = errors.New("not scripted in this test")

func (noCreation) VMSwitches(context.Context) ([]string, error) { return nil, errNotScripted }
func (noCreation) VMCreate(context.Context, CreateSpec) (string, error) {
	return "", errNotScripted
}
func (noCreation) VMAddNetwork(context.Context, string, string) (string, error) {
	return "", errNotScripted
}
func (noCreation) VMReplaceDisk(context.Context, string, string, int64) (string, error) {
	return "", errNotScripted
}
func (noCreation) VMDestroy(context.Context, string) (string, error) { return "", errNotScripted }
