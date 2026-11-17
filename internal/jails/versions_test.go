package jails

import (
	"context"
	"errors"
	"testing"

	"github.com/ottermq/jaildeck/internal/osversion"
)

// listingSystem is a host with the jails given, for tests of the listing.
type listingSystem struct {
	stubSystem
	jails []Jail
}

func (s listingSystem) List(context.Context) ([]Jail, error) {
	return append([]Jail(nil), s.jails...), nil
}

type stubVersions struct {
	host  osversion.Host
	trees map[string]string
	reads int
}

func (s *stubVersions) HostVersions(context.Context) (osversion.Host, error) { return s.host, nil }

func (s *stubVersions) Userland(_ context.Context, root string) (string, error) {
	s.reads++
	if v, ok := s.trees[root]; ok {
		return v, nil
	}
	return "", errors.New("not a freebsd-version script")
}

func versionedService(versions *stubVersions) *JailService {
	svc := NewJailService(Dependencies{System: listingSystem{jails: []Jail{
		{Name: "web", Status: JailStatusRunning, Path: "/jails/web"},
		{Name: "db", Status: JailStatusStopped, Path: "/jails/db"},
		{Name: "broken", Status: JailStatusRunning, Path: "/jails/broken"},
		{Name: "pathless", Status: JailStatusStopped},
	}}, Audit: &fakeLogger{}})
	svc.SetVersions(versions)
	return svc
}

func someTrees() *stubVersions {
	return &stubVersions{
		host:  osversion.NewHost("15.1-RELEASE-p6", "15.1-RELEASE-p6", "15.1-RELEASE-p6"),
		trees: map[string]string{"/jails/web": "15.1-RELEASE-p6", "/jails/db": "15.1-RELEASE-p2"},
	}
}

func TestListGivesEveryJailItsUserland(t *testing.T) {
	versions := someTrees()
	jails, err := versionedService(versions).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const host = "15.1-RELEASE-p6"
	want := map[string]osversion.Standing{
		"web": {Version: host, Status: osversion.Current, Host: host},
		// A stopped jail is read too: its files are there whether it runs
		// or not, and a stopped jail is still started one day.
		"db":       {Version: "15.1-RELEASE-p2", Status: osversion.Behind, Levels: 4, Host: host},
		"broken":   {Status: osversion.Unknown, Host: host},
		"pathless": {Status: osversion.Unknown, Host: host},
	}
	for _, jail := range jails {
		if jail.Userland == nil || *jail.Userland != want[jail.Name] {
			t.Errorf("%s: userland = %+v; want %+v", jail.Name, jail.Userland, want[jail.Name])
		}
	}
	// Three trees, three reads; a jail with no path costs none.
	if versions.reads != 3 {
		t.Errorf("reads = %d; want 3", versions.reads)
	}
}

func TestGetGivesTheJailItsUserland(t *testing.T) {
	jail, err := versionedService(someTrees()).Get(context.Background(), "db")
	if err != nil {
		t.Fatal(err)
	}
	if jail.Userland == nil || jail.Userland.Status != osversion.Behind || jail.Userland.Levels != 4 {
		t.Errorf("userland = %+v; want 4 behind", jail.Userland)
	}
}

func TestListWithoutAVersionSourceHasNoUserland(t *testing.T) {
	svc := NewJailService(Dependencies{System: listingSystem{jails: []Jail{{Name: "web", Path: "/jails/web"}}},
		Audit: &fakeLogger{}})
	jails, err := svc.List(context.Background())
	if err != nil || len(jails) != 1 || jails[0].Userland != nil {
		t.Fatalf("List = %+v, %v; want the jail with no userland", jails, err)
	}
}
