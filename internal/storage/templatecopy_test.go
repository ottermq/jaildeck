package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// A refreshed template is a copy of its parent's @base, not a clone (#258):
// nothing then keeps the parent from being removed.
func TestCopyMakesAnIndependentTemplate(t *testing.T) {
	system := withConfiguredJail()
	logger := &fakeLogger{}
	source := NewTemplateSource(NewService(system, logger, "tank"))

	root, err := source.Copy(context.Background(), "tank/jails/templates/15.1-RELEASE@base", "15.1-RELEASE-p7", "15.1-RELEASE")
	if err != nil {
		t.Fatal(err)
	}
	if root != "/usr/local/jails/templates/15.1-RELEASE-p7" {
		t.Errorf("root %q", root)
	}
	want := []string{
		"zfs send tank/jails/templates/15.1-RELEASE@base | zfs receive tank/jails/templates/15.1-RELEASE-p7@refresh-copy",
		// Never @base: the copy is not ready until it has been refreshed.
		"zfs destroy tank/jails/templates/15.1-RELEASE-p7@refresh-copy",
		"zfs set jaildeck:refreshed-from=15.1-RELEASE tank/jails/templates/15.1-RELEASE-p7",
	}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran\n  %q\nwant\n  %q", system.ran, want)
	}
	if len(logger.entries) < 3 {
		t.Errorf("wrote %d audit entries, want one per command", len(logger.entries))
	}

	// Listed with its parent from the property, not ready, and with no
	// origin: the parent's dependents do not include it.
	listed, err := source.Templates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, template := range listed {
		if template.Name == "15.1-RELEASE-p7" {
			if template.Parent != "15.1-RELEASE" || template.Ready {
				t.Errorf("listed %+v", template)
			}
			return
		}
	}
	t.Errorf("not listed: %+v", listed)
}

func TestCopyStopsWhereItFails(t *testing.T) {
	system := withConfiguredJail()
	system.cloneErr = errors.New("cannot receive new filesystem stream: destination exists")
	source := NewTemplateSource(NewService(system, &fakeLogger{}, "tank"))

	if _, err := source.Copy(context.Background(), "tank/jails/templates/15.1-RELEASE@base", "taken", "15.1-RELEASE"); err == nil {
		t.Fatal("no error")
	}
	if len(system.ran) != 1 {
		t.Errorf("went on after the receive failed: %q", system.ran)
	}
}

// Supersede marks the parent, and the listing reads the mark back (#259).
func TestSupersedeMarksAndListsTheParent(t *testing.T) {
	system := withConfiguredJail()
	source := NewTemplateSource(NewService(system, &fakeLogger{}, "tank"))

	if err := source.Supersede(context.Background(), "15.1-RELEASE", "15.1-RELEASE-p7"); err != nil {
		t.Fatal(err)
	}
	want := []string{"zfs set jaildeck:superseded-by=15.1-RELEASE-p7 tank/jails/templates/15.1-RELEASE"}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran %q, want %q", system.ran, want)
	}
	listed, err := source.Templates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, template := range listed {
		if template.Name == "15.1-RELEASE" {
			if template.SupersededBy != "15.1-RELEASE-p7" {
				t.Errorf("listed %+v", template)
			}
			return
		}
	}
	t.Errorf("not listed: %+v", listed)
}

// A refresh's orphans are recorded on the new template and listed back
// (#264); none means nothing is written.
func TestRecordOrphans(t *testing.T) {
	system := withConfiguredJail()
	source := NewTemplateSource(NewService(system, &fakeLogger{}, "tank"))

	if err := source.RecordOrphans(context.Background(), "15.1-RELEASE", nil); err != nil || len(system.ran) != 0 {
		t.Fatalf("no orphans: ran %q, %v", system.ran, err)
	}
	if err := source.RecordOrphans(context.Background(), "15.1-RELEASE", []string{"binutils", "gmp"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"zfs set jaildeck:orphans=binutils,gmp tank/jails/templates/15.1-RELEASE"}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran %q, want %q", system.ran, want)
	}
	listed, err := source.Templates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, template := range listed {
		if template.Name == "15.1-RELEASE" && !reflect.DeepEqual(template.Orphans, []string{"binutils", "gmp"}) {
			t.Errorf("listed %+v", template)
		}
	}
}

// Removing a template records its name on the templates parent (#268),
// once and most recent last, so a refresh can avoid suggesting it.
func TestDestroyRecordsTheRemovedName(t *testing.T) {
	system := withConfiguredJail()
	system.properties = append(system.properties, Property{
		Dataset: "tank/jails/templates", Name: "jaildeck:removed-templates", Value: "15.1-RELEASE,old"})
	source := NewTemplateSource(NewService(system, &fakeLogger{}, "tank"))

	if err := source.Destroy(context.Background(), "15.1-RELEASE"); err != nil {
		t.Fatal(err)
	}
	want := "zfs set jaildeck:removed-templates=old,15.1-RELEASE tank/jails/templates"
	if len(system.ran) != 2 || system.ran[1] != want {
		t.Errorf("ran %q, want the destroy then %q", system.ran, want)
	}
	removed, err := source.Removed(context.Background())
	if err != nil || !reflect.DeepEqual(removed, []string{"old", "15.1-RELEASE"}) {
		t.Errorf("Removed = %q, %v", removed, err)
	}
}

// The list is a naming hint, not a history: it keeps the newest names.
func TestRemovedNamesAreBounded(t *testing.T) {
	system := withConfiguredJail()
	names := make([]string, 0, 40)
	for i := range 40 {
		names = append(names, fmt.Sprintf("t%02d", i))
	}
	system.properties = append(system.properties, Property{
		Dataset: "tank/jails/templates", Name: "jaildeck:removed-templates", Value: strings.Join(names, ",")})
	source := NewTemplateSource(NewService(system, &fakeLogger{}, "tank"))

	if err := source.Destroy(context.Background(), "15.1-RELEASE"); err != nil {
		t.Fatal(err)
	}
	removed, _ := source.Removed(context.Background())
	if len(removed) != 32 || removed[31] != "15.1-RELEASE" || removed[0] != "t09" {
		t.Errorf("kept %d, first %q, last %q", len(removed), removed[0], removed[len(removed)-1])
	}
}

// An empty replacement lifts the mark with zfs inherit, not an empty value
// (#284): an empty jaildeck:superseded-by would still be a local property.
func TestSupersedeWithNothingLiftsTheMark(t *testing.T) {
	system := withConfiguredJail()
	source := NewTemplateSource(NewService(system, &fakeLogger{}, "tank"))
	if err := source.Supersede(context.Background(), "15.1-RELEASE", "15.1-RELEASE-p7"); err != nil {
		t.Fatal(err)
	}
	system.ran = nil
	if err := source.Supersede(context.Background(), "15.1-RELEASE", ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"zfs inherit jaildeck:superseded-by tank/jails/templates/15.1-RELEASE"}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran %q, want %q", system.ran, want)
	}
}
