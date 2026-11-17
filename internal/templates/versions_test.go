package templates

import (
	"context"
	"errors"
	"testing"

	"github.com/ottermq/jaildeck/internal/osversion"
)

type stubVersions struct {
	host  osversion.Host
	trees map[string]string
}

func (s stubVersions) HostVersions(context.Context) (osversion.Host, error) { return s.host, nil }

func (s stubVersions) Userland(_ context.Context, root string) (string, error) {
	if v, ok := s.trees[root]; ok {
		return v, nil
	}
	return "", errors.New("no such tree")
}

func TestListGivesEachTemplateWithATreeItsUserland(t *testing.T) {
	storage := release()
	storage.templates = append(storage.templates,
		Template{Name: "pg18", Dataset: "tank/jails/templates/pg18", Mountpoint: "/usr/local/jails/templates/pg18", Ready: true},
		Template{Name: "unmounted", Dataset: "tank/jails/templates/unmounted", Mountpoint: "none"})
	service := NewService(storage, &stubPackages{}, &stubReleases{fetched: []string{"14.3-RELEASE"}}, &fakeLogger{})
	service.SetVersions(stubVersions{
		host: osversion.NewHost("15.1-RELEASE-p6", "15.1-RELEASE-p6", "15.1-RELEASE-p6"),
		trees: map[string]string{
			"/usr/local/jails/templates/15.1-RELEASE": "15.1-RELEASE-p5",
			"/usr/local/jails/templates/pg18":         "15.1-RELEASE-p2",
		},
	})

	found, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]*osversion.Standing{
		"15.1-RELEASE": {Version: "15.1-RELEASE-p5", Status: osversion.Behind, Levels: 1, Host: "15.1-RELEASE-p6"},
		"pg18":         {Version: "15.1-RELEASE-p2", Status: osversion.Behind, Levels: 4, Host: "15.1-RELEASE-p6"},
		// Not mounted, and only fetched: no tree, so no version at all
		// rather than "unknown".
		"unmounted":    nil,
		"14.3-RELEASE": nil,
	}
	if len(found) != len(want) {
		t.Fatalf("List = %d templates; want %d", len(found), len(want))
	}
	for _, template := range found {
		w, ok := want[template.Name]
		if !ok {
			t.Errorf("unexpected template %q", template.Name)
			continue
		}
		switch {
		case w == nil && template.Userland != nil:
			t.Errorf("%s: userland = %+v; want none", template.Name, *template.Userland)
		case w != nil && (template.Userland == nil || *template.Userland != *w):
			t.Errorf("%s: userland = %+v; want %+v", template.Name, template.Userland, *w)
		}
	}
}

func TestListWithoutAVersionSource(t *testing.T) {
	found, err := NewService(release(), &stubPackages{}, &stubReleases{}, &fakeLogger{}).List(context.Background())
	if err != nil || len(found) != 1 || found[0].Userland != nil {
		t.Fatalf("List = %+v, %v; want the template with no userland", found, err)
	}
}
