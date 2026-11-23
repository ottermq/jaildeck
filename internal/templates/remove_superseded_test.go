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
	if len(removal.Replaced) != 1 || removal.Replaced[0].Template != "base-1" || removal.Replaced[0].Error == "" {
		t.Errorf("Replaced = %+v, want base-1 with the error", removal.Replaced)
	}
	if removal.Replaced[0].Error != "internal server error" {
		t.Errorf("Error = %q leaks the underlying text", removal.Replaced[0].Error)
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

// A loop through the removed template is lifted, never turned into a mark on
// itself.
func TestRemovingATemplateInALoopLiftsTheMark(t *testing.T) {
	storage := removable()
	storage.templates = append(storage.templates,
		Template{Name: "a", Dataset: "tank/jails/templates/a", Ready: true, SupersededBy: "b"},
		Template{Name: "b", Dataset: "tank/jails/templates/b", Ready: true, SupersededBy: "a"})
	removal := remove(t, storage, "b")
	if want := []Replacement{{Template: "a", SupersededBy: ""}}; !reflect.DeepEqual(removal.Replaced, want) {
		t.Errorf("Replaced = %+v, want %+v", removal.Replaced, want)
	}
}

// Every template naming the removed one is moved on, and a failure on one
// does not stop the others.
func TestEveryMarkNamingTheRemovedOneIsMoved(t *testing.T) {
	storage := withChain()
	storage.templates = append(storage.templates,
		Template{Name: "base-other", Dataset: "tank/jails/templates/base-other", Ready: true, SupersededBy: "base-2"})
	removal := remove(t, storage, "base-2")
	if len(removal.Replaced) != 2 {
		t.Fatalf("Replaced = %+v, want base-1 and base-other", removal.Replaced)
	}
}

// A template of the same name on another pool is not the one a mark means.
func TestOnlySiblingsAreMoved(t *testing.T) {
	storage := withChain()
	storage.templates = append(storage.templates,
		Template{Name: "elsewhere", Dataset: "zroot/jails/templates/elsewhere", Ready: true, SupersededBy: "base-2"})
	removal := remove(t, storage, "base-2")
	for _, r := range removal.Replaced {
		if r.Template == "elsewhere" {
			t.Errorf("moved a template on another pool: %+v", r)
		}
	}
}

// A dangling or self mark is not listed as superseded (#284).
func TestTheListingDropsDanglingMarks(t *testing.T) {
	storage := removable()
	storage.templates = append(storage.templates,
		Template{Name: "a", Dataset: "tank/jails/templates/a", Ready: true, SupersededBy: "gone"},
		Template{Name: "b", Dataset: "tank/jails/templates/b", Ready: true, SupersededBy: "b"})
	listed, err := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if got, _ := byName(listed, name); got.SupersededBy != "" {
			t.Errorf("%s listed as superseded by %q", name, got.SupersededBy)
		}
	}
}
