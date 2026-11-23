package templates

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// A chain: base, superseded by base-1, superseded by base-2.
func withChain() *stubStorage {
	s := removable()
	s.templates = append(s.templates,
		Template{Name: "base", Dataset: "tank/jails/templates/base", Ready: true, SupersededBy: "base-1"},
		Template{Name: "base-1", Dataset: "tank/jails/templates/base-1", Ready: true, SupersededBy: "base-2"},
		Template{Name: "base-2", Dataset: "tank/jails/templates/base-2", Ready: true},
	)
	return s
}

func remove(t *testing.T, storage *stubStorage, name string) Removal {
	t.Helper()
	removal, err := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).
		Remove(context.Background(), name, name)
	if err != nil {
		t.Fatalf("Remove(%s) error = %v", name, err)
	}
	return removal
}

// Removing the newest lifts the mark that named it (#284).
func TestRemovingTheReplacementLiftsTheMark(t *testing.T) {
	storage := withChain()
	removal := remove(t, storage, "base-2")
	want := []Replacement{{Template: "base-1", SupersededBy: ""}}
	if !reflect.DeepEqual(removal.Replaced, want) {
		t.Errorf("Replaced = %+v, want %+v", removal.Replaced, want)
	}
	if got, _ := byName(storage.templates, "base-1"); got.SupersededBy != "" {
		t.Errorf("base-1 is still superseded by %q", got.SupersededBy)
	}
}

// Removing one in the middle points the older one at what replaced it.
func TestRemovingAMiddleTemplateMovesTheMarkOn(t *testing.T) {
	storage := withChain()
	removal := remove(t, storage, "base-1")
	want := []Replacement{{Template: "base", SupersededBy: "base-2"}}
	if !reflect.DeepEqual(removal.Replaced, want) {
		t.Errorf("Replaced = %+v, want %+v", removal.Replaced, want)
	}
	if got, _ := byName(storage.templates, "base"); got.SupersededBy != "base-2" {
		t.Errorf("base is superseded by %q, want base-2", got.SupersededBy)
	}
}

// A mark that cannot be changed is reported, still naming the removed one.
func TestAMarkThatCannotBeChangedIsReported(t *testing.T) {
	storage := withChain()
	storage.supersedeErr = errors.New("permission denied")
	removal := remove(t, storage, "base-2")
	if len(removal.Replaced) != 1 || removal.Replaced[0].SupersededBy != "base-2" || removal.Replaced[0].Error == "" {
		t.Errorf("Replaced = %+v, want base-1 still naming base-2, with the error", removal.Replaced)
	}
	if !removal.Destroyed {
		t.Error("the removal is reported as not done")
	}
}

// Nothing named it: nothing to move, and the list is empty, not null.
func TestRemovingAnUnreplacingTemplateMovesNothing(t *testing.T) {
	removal := remove(t, removable(), "postgres-16")
	if removal.Replaced == nil || len(removal.Replaced) != 0 {
		t.Errorf("Replaced = %#v, want an empty list", removal.Replaced)
	}
}
