package storage

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// A template marked superseded is refused as the source of a new jail and of
// a reclone (#259), naming the newest replacement down the chain; clearing
// the mark lifts the refusal on the next request.
func TestASupersededTemplateIsRefusedAsACloneSource(t *testing.T) {
	system := withContainersParent()
	mark := func(dataset, by string) {
		system.properties = append(system.properties, Property{Dataset: dataset, Name: "jaildeck:superseded-by", Value: by})
	}
	for _, name := range []string{"15.1-RELEASE-a", "15.1-RELEASE-b"} {
		system.datasets = append(system.datasets,
			Dataset{Name: "tank/jails/templates/" + name, Mountpoint: "/usr/local/jails/templates/" + name})
	}
	mark("tank/jails/templates/15.1-RELEASE", "15.1-RELEASE-a")
	mark("tank/jails/templates/15.1-RELEASE-a", "15.1-RELEASE-b")

	checks := map[string]func() error{
		"new jail": func() error {
			return jailSourceFor(system, &fakeLogger{}).CheckTemplate(context.Background(), createTemplate)
		},
		"reclone": func() error {
			return NewService(system, &fakeLogger{}, "tank").
				CheckRecloneSource(context.Background(), "tank/jails/containers/db1", createTemplate)
		},
	}
	for what, check := range checks {
		err := check()
		if code := appErrCode(t, err); code != http.StatusConflict {
			t.Fatalf("%s: got %v, want a 409", what, err)
		}
		for _, want := range []string{"superseded by 15.1-RELEASE-b", "tank/jails/templates/15.1-RELEASE-b@base",
			"zfs inherit jaildeck:superseded-by tank/jails/templates/15.1-RELEASE"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q does not say %q", what, err, want)
			}
		}
	}

	system.properties = nil
	for what, check := range checks {
		if err := check(); err != nil {
			t.Errorf("%s after clearing the mark: %v", what, err)
		}
	}
}

// A mark set by hand into a loop ends the chain rather than spinning.
func TestASupersededLoopEnds(t *testing.T) {
	system := withContainersParent()
	system.datasets = append(system.datasets,
		Dataset{Name: "tank/jails/templates/other", Mountpoint: "/usr/local/jails/templates/other"})
	system.properties = []Property{
		{Dataset: "tank/jails/templates/15.1-RELEASE", Name: "jaildeck:superseded-by", Value: "other"},
		{Dataset: "tank/jails/templates/other", Name: "jaildeck:superseded-by", Value: "15.1-RELEASE"},
	}
	err := jailSourceFor(system, &fakeLogger{}).CheckTemplate(context.Background(), createTemplate)
	if appErrCode(t, err) != http.StatusConflict || !strings.Contains(err.Error(), "superseded by other;") {
		t.Fatalf("got %v", err)
	}
}

// A mark naming a template that is gone, or the template itself, refuses
// nothing (#284): there is nothing to use instead.
func TestADanglingOrSelfMarkRefusesNothing(t *testing.T) {
	for _, by := range []string{"removed-long-ago", "15.1-RELEASE"} {
		system := withContainersParent()
		system.properties = []Property{{Dataset: "tank/jails/templates/15.1-RELEASE", Name: "jaildeck:superseded-by", Value: by}}
		if err := jailSourceFor(system, &fakeLogger{}).CheckTemplate(context.Background(), createTemplate); err != nil {
			t.Errorf("marked %q: %v", by, err)
		}
	}
}
