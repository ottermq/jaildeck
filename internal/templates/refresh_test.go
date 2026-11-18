package templates

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/osversion"
)

// A storage with one template, pg18, made however built says, its @base
// reading 15.1-RELEASE-p2.
func refreshable(built string) (*stubStorage, *stubReleases, *stubPackages, *Service) {
	storage := &stubStorage{templates: []Template{{
		Name: "pg18", Dataset: "tank/jails/templates/pg18", Mountpoint: "/usr/local/jails/templates/pg18",
		Ready: true, Snapshot: "tank/jails/templates/pg18@base", Built: built,
	}}}
	releases := &stubReleases{}
	packages := &stubPackages{}
	service := NewService(storage, packages, releases, &fakeLogger{})
	service.SetVersions(stubVersions{
		host:  osversion.NewHost("15.1-RELEASE-p6", "15.1-RELEASE-p6", "15.1-RELEASE-p6"),
		trees: map[string]string{"/usr/local/jails/templates/pg18/.zfs/snapshot/base": "15.1-RELEASE-p2"},
	})
	return storage, releases, packages, service
}

// waitIdle waits for the detached refresh to finish.
func waitIdle(t *testing.T, s *Service, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, busy := s.running.of(name); !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s still running", name)
}

func TestRefreshBuildsANewTemplate(t *testing.T) {
	storage, releases, packages, service := refreshable(BuiltDerived)
	got, err := service.Refresh(context.Background(), "pg18-p7", "pg18")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "pg18-p7" && got.State != StateUpdating {
		t.Errorf("first answer %+v", got)
	}
	waitIdle(t, service, "pg18-p7")

	// Copied, not cloned (#258), with the parent's name recorded.
	storage.mu.Lock()
	copied, derived := append([]string(nil), storage.copied...), len(storage.derived)
	storage.mu.Unlock()
	if !slices.Equal(copied, []string{"tank/jails/templates/pg18@base => pg18-p7 (from pg18)"}) || derived != 0 {
		t.Errorf("copied %v, derived %d", copied, derived)
	}
	// freebsd-update is told the plain release, read from the parent's @base.
	if got := releases.brought(); !slices.Equal(got, []string{"15.1-RELEASE"}) {
		t.Errorf("updated %v", got)
	}
	packages.mu.Lock()
	upgraded := append([]string(nil), packages.upgraded...)
	packages.mu.Unlock()
	if !slices.Equal(upgraded, []string{"/usr/local/jails/templates/pg18-p7"}) {
		t.Errorf("upgraded %v", upgraded)
	}
	// Marked before it is sealed, and the parent never touched.
	if got := storage.markedTemplates(); !slices.Equal(got, []string{"pg18-p7=refreshed"}) {
		t.Errorf("marked %v", got)
	}
	if got := storage.sealedTemplates(); !slices.Equal(got, []string{"pg18-p7"}) {
		t.Errorf("sealed %v", got)
	}
}

// Every template Jail Deck builds that cannot vouch for its own files is
// refused: one promoted from a jail, and one that does not say.
func TestRefreshRefusesATreeJailDeckDidNotBuild(t *testing.T) {
	for built, want := range map[string]string{BuiltJail: "made from a jail", "": "zfs set jaildeck:built=derived"} {
		storage, _, _, service := refreshable(built)
		_, err := service.Refresh(context.Background(), "pg18-p7", "pg18")
		if appErrCode(t, err) != http.StatusConflict || !strings.Contains(err.Error(), want) {
			t.Errorf("built %q: %v", built, err)
		}
		if len(storage.copied) != 0 {
			t.Errorf("built %q: copied anyway", built)
		}
	}
}

func TestRefreshStopsWhereAStageFails(t *testing.T) {
	storage, releases, _, service := refreshable(BuiltRelease)
	releases.updateErr = context.DeadlineExceeded
	if _, err := service.Refresh(context.Background(), "pg18-p7", "pg18"); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, service, "pg18-p7")
	// Cloned, not sealed: shown as not ready, as a failed derive is.
	if len(storage.sealedTemplates()) != 0 || len(storage.markedTemplates()) != 0 {
		t.Errorf("sealed %v marked %v after a failed update", storage.sealedTemplates(), storage.markedTemplates())
	}
	// And a repeat is refused rather than built on top of it.
	if _, err := service.Refresh(context.Background(), "pg18-p7", "pg18"); appErrCode(t, err) != http.StatusConflict {
		t.Errorf("repeat after failure: %v", err)
	}
}

func TestRefreshIsIdempotentOnceReady(t *testing.T) {
	storage, _, _, service := refreshable(BuiltDerived)
	storage.templates = append(storage.templates, Template{Name: "pg18-p7", Dataset: "tank/jails/templates/pg18-p7",
		Mountpoint: "/usr/local/jails/templates/pg18-p7", Ready: true})
	got, err := service.Refresh(context.Background(), "pg18-p7", "pg18")
	if err != nil || got.Name != "pg18-p7" || len(storage.copied) != 0 {
		t.Fatalf("got %+v, %v; copied %v", got, err, storage.copied)
	}
}

func TestRefreshNeedsAKnownRelease(t *testing.T) {
	_, _, _, service := refreshable(BuiltDerived)
	service.SetVersions(stubVersions{host: osversion.NewHost("15.1-RELEASE-p6", "15.1-RELEASE-p6", "15.1-RELEASE-p6")})
	if _, err := service.Refresh(context.Background(), "pg18-p7", "pg18"); appErrCode(t, err) != http.StatusConflict {
		t.Fatalf("got %v", err)
	}
}
