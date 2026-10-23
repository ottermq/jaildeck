package storage

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const configuredJail = "tank/jails/containers/ottermq"

// A jail as one really is: a clone of the release template, configured since.
func withConfiguredJail() *stubSystem {
	return &stubSystem{
		pools: []string{"tank"},
		datasets: []Dataset{
			{Name: "tank/jails", Mountpoint: "/usr/local/jails"},
			{Name: "tank/jails/templates", Mountpoint: "/usr/local/jails/templates"},
			{Name: "tank/jails/templates/15.1-RELEASE", Mountpoint: "/usr/local/jails/templates/15.1-RELEASE"},
			{Name: configuredJail, Mountpoint: "/usr/local/jails/containers/ottermq"},
		},
		snapshots: []Snapshot{{
			Name: "tank/jails/templates/15.1-RELEASE@base", Dataset: "tank/jails/templates/15.1-RELEASE",
			Short: "base", CreateTXG: 10,
		}},
		origins:    map[string]string{configuredJail: "tank/jails/templates/15.1-RELEASE@base"},
		clones:     map[string][]string{},
		notMounted: map[string]bool{},
		nextTXG:    100,
	}
}

func TestFromJailSnapshotsClonesAndPromotes(t *testing.T) {
	system := withConfiguredJail()
	logger := &fakeLogger{}

	target, err := NewTemplateSource(NewService(system, logger, "tank")).
		FromJail(context.Background(), configuredJail, "ottermq-base")
	if err != nil {
		t.Fatalf("FromJail() error = %v", err)
	}
	if target != "tank/jails/templates/ottermq-base" {
		t.Errorf("target = %q", target)
	}

	want := []string{
		"zfs snapshot " + configuredJail + "@base",
		"zfs clone " + configuredJail + "@base tank/jails/templates/ottermq-base",
		"zfs promote tank/jails/templates/ottermq-base",
	}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran\n  %q\nwant\n  %q", system.ran, want)
	}
	if len(logger.entries) < 3 {
		t.Errorf("wrote %d audit entries, want one per command", len(logger.entries))
	}
}

// The reason the promote is there, asserted as the dependency it moves. Leave
// it out and the jail can never be destroyed: zfs answers "filesystem has
// dependent clones" and names the template (measured on the deployment
// target). This is the test that would catch its removal.
func TestFromJailLeavesTheJailDependingOnTheTemplate(t *testing.T) {
	system := withConfiguredJail()

	if _, err := NewTemplateSource(NewService(system, &fakeLogger{}, "tank")).
		FromJail(context.Background(), configuredJail, "ottermq-base"); err != nil {
		t.Fatalf("FromJail() error = %v", err)
	}

	const template = "tank/jails/templates/ottermq-base"
	if got := system.origins[configuredJail]; got != template+"@base" {
		t.Errorf("the jail's origin is %q, want it to be a clone of the template (%s@base). "+
			"The other way round pins the jail forever.", got, template)
	}
	// And the template hangs off the release template, so the lineage reads
	// 15.1-RELEASE -> ottermq-base -> ottermq, which is JD-007's model.
	if got := system.origins[template]; got != "tank/jails/templates/15.1-RELEASE@base" {
		t.Errorf("the template's origin is %q, want the release template", got)
	}
	// The snapshot moved with the ownership, which is what makes @base the
	// template's rather than the jail's.
	for _, snapshot := range system.snapshots {
		if snapshot.Name == configuredJail+"@base" {
			t.Error("@base is still on the jail; the promote did not move it")
		}
	}
}

// Listing it afterwards shows it ready, with its parent -- all read from ZFS,
// so the new template needs no special case anywhere.
func TestATemplateMadeFromAJailListsLikeAnyOther(t *testing.T) {
	system := withConfiguredJail()
	service := NewService(system, &fakeLogger{}, "tank")
	source := NewTemplateSource(service)

	if _, err := source.FromJail(context.Background(), configuredJail, "ottermq-base"); err != nil {
		t.Fatalf("FromJail() error = %v", err)
	}
	listed, err := source.Templates(context.Background())
	if err != nil {
		t.Fatalf("Templates() error = %v", err)
	}

	var found bool
	for _, template := range listed {
		if template.Name != "ottermq-base" {
			continue
		}
		found = true
		if !template.Ready {
			t.Error("the template is not ready, but @base moved to it on promote")
		}
		if template.Parent != "15.1-RELEASE" {
			t.Errorf("parent = %q, want 15.1-RELEASE", template.Parent)
		}
	}
	if !found {
		t.Errorf("ottermq-base is not in the listing: %+v", listed)
	}
}

func TestFromJailRefusals(t *testing.T) {
	t.Run("the template name is taken", func(t *testing.T) {
		system := withConfiguredJail()
		system.datasets = append(system.datasets, Dataset{Name: "tank/jails/templates/taken"})

		_, err := NewTemplateSource(NewService(system, &fakeLogger{}, "tank")).
			FromJail(context.Background(), configuredJail, "taken")
		if err == nil {
			t.Fatal("FromJail() returned no error")
		}
		if code := appErrCode(t, err); code != http.StatusConflict {
			t.Errorf("error code = %d, want 409", code)
		}
		if len(system.ran) != 0 {
			t.Errorf("ran %q for a refused call, want nothing", system.ran)
		}
	})

	t.Run("the jail already carries a @base", func(t *testing.T) {
		system := withConfiguredJail()
		system.snapshots = append(system.snapshots, Snapshot{
			Name: configuredJail + "@base", Dataset: configuredJail, Short: "base", CreateTXG: 50,
		})
		_, err := NewTemplateSource(NewService(system, &fakeLogger{}, "tank")).
			FromJail(context.Background(), configuredJail, "ottermq-base")
		if err == nil {
			t.Fatal("FromJail() returned no error for a jail that already has @base")
		}
		for _, command := range system.ran {
			if strings.HasPrefix(command, "zfs clone") {
				t.Errorf("cloned despite the snapshot failing: %q", system.ran)
			}
		}
	})

	t.Run("a failed promote is reported, not unwound", func(t *testing.T) {
		system := withConfiguredJail()
		system.promoteErr = errors.New("cannot promote: pool is busy")

		_, err := NewTemplateSource(NewService(system, &fakeLogger{}, "tank")).
			FromJail(context.Background(), configuredJail, "ottermq-base")
		if err == nil {
			t.Fatal("FromJail() returned no error")
		}
		if !strings.Contains(err.Error(), "promote") {
			t.Errorf("error = %q, want it to name the step that failed", err)
		}
	})
}
