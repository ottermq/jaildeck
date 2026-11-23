package storage

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func withTaggedJailToReclone() *stubSystem {
	system := withJailToReclone()
	system.properties = append(system.properties,
		Property{Dataset: recloneRoot, Name: "acme:role", Value: "cluster"},
		Property{Dataset: recloneRoot, Name: "acme:tenant", Value: ""},
		Property{Dataset: recloneRoot, Name: "jaildeck:mountpoint", Value: "var/db"})
	return system
}

// The old root's own user properties come across to the new one, Jail Deck's
// own excepted, and the parked root keeps all of them (#281).
func TestRecloneCarriesTheRootsUserProperties(t *testing.T) {
	system := withTaggedJailToReclone()

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err != nil {
		t.Fatalf("RecloneRoot() error = %v", err)
	}
	if want := []string{"acme:role", "acme:tenant"}; !slices.Equal(result.Properties, want) {
		t.Errorf("Properties = %v, want %v", result.Properties, want)
	}
	if got := system.localValue(recloneRoot, "acme:role"); got != "cluster" {
		t.Errorf("acme:role on the new root = %q, want cluster", got)
	}
	if !slices.Contains(system.properties, Property{Dataset: recloneRoot, Name: "acme:tenant", Value: ""}) {
		t.Error("an empty value was not carried as empty")
	}
	if got := system.localValue(recloneRoot, "jaildeck:mountpoint"); got != "" {
		t.Errorf("jaildeck:mountpoint was copied to the new root as %q", got)
	}
	if got := system.localValue(result.OldRoot, "acme:role"); got != "cluster" {
		t.Errorf("the parked root lost acme:role: %q", got)
	}
}

// A property the new root already sets itself is kept and reported.
func TestRecloneKeepsAPropertyTheNewRootSets(t *testing.T) {
	system := withTaggedJailToReclone()
	system.cloneProps = map[string]string{"acme:role": "template-default"}

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err != nil {
		t.Fatalf("RecloneRoot() error = %v", err)
	}
	if !slices.Equal(result.PropertiesKept, []string{"acme:role"}) || slices.Contains(result.Properties, "acme:role") {
		t.Errorf("Properties = %v, PropertiesKept = %v", result.Properties, result.PropertiesKept)
	}
	if got := system.localValue(recloneRoot, "acme:role"); got != "template-default" {
		t.Errorf("acme:role = %q, want the new root's own", got)
	}
}

// A failed copy stops before the children come back, and says where they are.
func TestRecloneStopsWhenThePropertiesCannotBeCopied(t *testing.T) {
	system := withTaggedJailToReclone()
	system.setErr = errors.New("cannot set property: permission denied")

	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err == nil {
		t.Fatal("RecloneRoot() returned no error")
	}
	if result.FailedAt != StepCopyProperties {
		t.Fatalf("FailedAt = %q, want %q", result.FailedAt, StepCopyProperties)
	}
	if len(result.Parked) != 2 || result.OldRoot == "" {
		t.Errorf("Parked = %v, OldRoot = %q; want both children and the old root", result.Parked, result.OldRoot)
	}
	if len(result.Properties) != 0 {
		t.Errorf("Properties = %v after a failed copy", result.Properties)
	}
}

// A root with no properties of its own costs no zfs set at all.
func TestRecloneWithNothingToCarrySetsNothing(t *testing.T) {
	system := withJailToReclone()
	result, err := NewService(system, &fakeLogger{}, "tank").
		RecloneRoot(context.Background(), recloneRoot, newTemplate)
	if err != nil {
		t.Fatal(err)
	}
	if result.Properties == nil || len(result.Properties) != 0 {
		t.Errorf("Properties = %#v, want an empty list", result.Properties)
	}
	for _, command := range system.ran {
		if len(command) > 7 && command[:7] == "zfs set" {
			t.Errorf("ran %q with nothing to carry", command)
		}
	}
}
