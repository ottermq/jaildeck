package templates

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

type stubStorage struct {
	templates []Template
	listErr   error
	derived   []string
	sealed    []string
	deriveErr error
	sealErr   error
	// The removal half.
	dependents    map[string]Dependents
	dependentsErr error
	destroyed     []string
	destroyErr    error
}

func (s *stubStorage) Templates(context.Context) ([]Template, error) {
	return s.templates, s.listErr
}

func (s *stubStorage) Derive(_ context.Context, parentSnapshot, name string) (string, error) {
	s.derived = append(s.derived, parentSnapshot+" -> "+name)
	if s.deriveErr != nil {
		return "", s.deriveErr
	}
	root := "/usr/local/jails/templates/" + name
	s.templates = append(s.templates, Template{
		Name: name, Dataset: "tank/jails/templates/" + name, Mountpoint: root,
		Parent: strings.TrimPrefix(strings.Split(parentSnapshot, "@")[0], "tank/jails/templates/"),
	})
	return root, nil
}

func (s *stubStorage) Seal(_ context.Context, name string) error {
	s.sealed = append(s.sealed, name)
	if s.sealErr != nil {
		return s.sealErr
	}
	for i, template := range s.templates {
		if template.Name == name {
			s.templates[i].Ready = true
			s.templates[i].Snapshot = template.Dataset + "@" + ReadySnapshot
		}
	}
	return nil
}

type stubPackages struct {
	installed []string
	err       error
}

// stubReleases is the mirror. Most tests here never reach it -- they are about
// deriving one template from another, which touches no release at all -- and
// Fetched returning nothing is what makes that true rather than assumed.
//
// Guarded, because the real thing is called from the goroutine Prepare
// detaches while the caller goes on reading the service. A stub that was not
// safe to share would be lying about the contract it stands in for, and -race
// says so.
type stubReleases struct {
	mu       sync.Mutex
	fetched  []string
	host     Host
	fetchErr error
	attempts []string
}

func (s *stubReleases) Host(context.Context) (Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host.Release == "" {
		return Host{Release: "15.1-RELEASE", Arch: "amd64"}, nil
	}
	return s.host, nil
}

func (s *stubReleases) Fetched(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.fetched...), nil
}

func (s *stubReleases) FetchBase(_ context.Context, release, _ string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, release)
	if s.fetchErr != nil {
		return Result{Command: "fetch " + release}, s.fetchErr
	}
	s.fetched = append(s.fetched, release)
	return Result{Root: "/media/" + release + "/base.txz", Command: "fetch " + release}, nil
}

// tried is what FetchBase was asked for, copied under the lock.
func (s *stubReleases) tried() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.attempts...)
}

var _ ReleaseSource = (*stubReleases)(nil)

func (s *stubPackages) Install(_ context.Context, root string, packages []string) (Result, error) {
	command := "pkg -c " + root + " install -y " + strings.Join(packages, " ")
	s.installed = append(s.installed, command)
	return Result{Root: root, Command: command}, s.err
}

type fakeLogger struct{ entries []audit.Entry }

func (f *fakeLogger) Log(_ context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return nil
}

func release() *stubStorage {
	return &stubStorage{templates: []Template{{
		Name: "15.1-RELEASE", Dataset: "tank/jails/templates/15.1-RELEASE",
		Mountpoint: "/usr/local/jails/templates/15.1-RELEASE",
		Ready:      true, Snapshot: "tank/jails/templates/15.1-RELEASE@base",
	}}}
}

func appErrCode(t *testing.T, err error) int {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error %v is not an *common.AppError", err)
	}
	return appErr.Code
}

// Clone the parent's @base, install into the clone, snapshot as @base. In that
// order: sealing before the packages are in would mark an incomplete template
// usable, and a jail cloned from it would be missing what it was built for.
func TestDeriveClonesInstallsAndSeals(t *testing.T) {
	storage, packages, logger := release(), &stubPackages{}, &fakeLogger{}

	template, built, err := NewService(storage, packages, &stubReleases{}, logger).
		Derive(context.Background(), "postgres-16", "15.1-RELEASE", []string{"postgresql16-server"})
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if !built {
		t.Error("built = false for a template that did not exist")
	}
	if !template.Ready || template.Parent != "15.1-RELEASE" {
		t.Errorf("template = %+v, want it ready with its parent named", template)
	}

	wantDerived := []string{"tank/jails/templates/15.1-RELEASE@base -> postgres-16"}
	if !reflect.DeepEqual(storage.derived, wantDerived) {
		t.Errorf("derived %q, want %q", storage.derived, wantDerived)
	}
	wantInstalled := []string{"pkg -c /usr/local/jails/templates/postgres-16 install -y postgresql16-server"}
	if !reflect.DeepEqual(packages.installed, wantInstalled) {
		t.Errorf("installed %q, want %q", packages.installed, wantInstalled)
	}
	if !reflect.DeepEqual(storage.sealed, []string{"postgres-16"}) {
		t.Errorf("sealed %q, want it sealed once after the install", storage.sealed)
	}
	if len(logger.entries) != 1 || logger.entries[0].Operation != OperationInstallPackages {
		t.Errorf("audit = %+v, want one install-packages entry", logger.entries)
	}
}

// The retry a client makes after a timeout.
func TestDeriveIsIdempotentForAReadyTemplate(t *testing.T) {
	storage := release()
	storage.templates = append(storage.templates, Template{
		Name: "postgres-16", Dataset: "tank/jails/templates/postgres-16", Ready: true,
		Snapshot: "tank/jails/templates/postgres-16@base", Parent: "15.1-RELEASE",
	})
	packages := &stubPackages{}

	template, built, err := NewService(storage, packages, &stubReleases{}, &fakeLogger{}).
		Derive(context.Background(), "postgres-16", "15.1-RELEASE", []string{"postgresql16-server"})
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if built {
		t.Error("built = true for a template that was already there")
	}
	if template.Name != "postgres-16" {
		t.Errorf("template = %+v", template)
	}
	if len(packages.installed) != 0 || len(storage.derived) != 0 {
		t.Errorf("ran %q and %q for a template that already existed", storage.derived, packages.installed)
	}
}

// A template that exists without its @base is a previous attempt that failed.
// Building on top of it would install packages over whatever it left behind.
func TestDeriveRefusesToFinishAnUnreadyTemplate(t *testing.T) {
	storage := release()
	storage.templates = append(storage.templates, Template{
		Name: "postgres-16", Dataset: "tank/jails/templates/postgres-16", Ready: false,
	})
	packages := &stubPackages{}

	_, _, err := NewService(storage, packages, &stubReleases{}, &fakeLogger{}).
		Derive(context.Background(), "postgres-16", "15.1-RELEASE", []string{"postgresql16-server"})
	if err == nil {
		t.Fatal("Derive() returned no error for an unready template")
	}
	if code := appErrCode(t, err); code != http.StatusConflict {
		t.Errorf("error code = %d, want 409", code)
	}
	if len(packages.installed) != 0 {
		t.Errorf("installed %q on top of a failed attempt", packages.installed)
	}
}

// A failed install leaves the clone without its @base, which the listing
// reports as not ready. That is the whole recovery story: no cleanup is needed
// for the state to be truthful (JD-027).
func TestAFailedInstallLeavesAnUnreadyTemplate(t *testing.T) {
	storage, logger := release(), &fakeLogger{}
	packages := &stubPackages{err: errors.New("pkg: No packages available to install matching 'nope'")}

	_, _, err := NewService(storage, packages, &stubReleases{}, logger).
		Derive(context.Background(), "postgres-16", "15.1-RELEASE", []string{"nope"})
	if err == nil {
		t.Fatal("Derive() returned no error")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("error = %q, want it to say the template exists and is not ready", err)
	}
	if len(storage.sealed) != 0 {
		t.Errorf("sealed %q after a failed install; the template must stay unusable", storage.sealed)
	}
	// And it is in the listing, unready, for the operator to find.
	found, _ := NewService(storage, packages, &stubReleases{}, logger).List(context.Background())
	for _, template := range found {
		if template.Name == "postgres-16" && template.Ready {
			t.Error("the half-built template reports itself ready")
		}
	}
	if entry := logger.entries[0]; entry.Success || entry.Error == "" {
		t.Errorf("audit entry = %+v, want a failure carrying the reason", entry)
	}
}

func TestDeriveRefusals(t *testing.T) {
	tests := []struct {
		name     string
		template string
		parent   string
		packages []string
		wantCode int
	}{
		{name: "no such parent", template: "x", parent: "14.2-RELEASE", wantCode: http.StatusNotFound},
		{name: "a name ZFS will not take", template: "-x", parent: "15.1-RELEASE", wantCode: http.StatusBadRequest},
		{name: "empty name", template: "", parent: "15.1-RELEASE", wantCode: http.StatusBadRequest},
		{name: "a slash", template: "a/b", parent: "15.1-RELEASE", wantCode: http.StatusBadRequest},
		// pkg would read these as options rather than packages.
		{name: "a package beginning with a dash", template: "x", parent: "15.1-RELEASE",
			packages: []string{"-f"}, wantCode: http.StatusBadRequest},
		{name: "a package with a space", template: "x", parent: "15.1-RELEASE",
			packages: []string{"a b"}, wantCode: http.StatusBadRequest},
		{name: "an empty package name", template: "x", parent: "15.1-RELEASE",
			packages: []string{""}, wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage, packages := release(), &stubPackages{}
			_, _, err := NewService(storage, packages, &stubReleases{}, &fakeLogger{}).
				Derive(context.Background(), tt.template, tt.parent, tt.packages)
			if err == nil {
				t.Fatal("Derive() returned no error")
			}
			if code := appErrCode(t, err); code != tt.wantCode {
				t.Errorf("error code = %d, want %d (%v)", code, tt.wantCode, err)
			}
			if len(storage.derived) != 0 || len(packages.installed) != 0 {
				t.Errorf("a refused derive ran %q and %q", storage.derived, packages.installed)
			}
		})
	}
}

// A parent without its @base is not usable as a parent (JD-007).
func TestDeriveRefusesAnUnreadyParent(t *testing.T) {
	storage := &stubStorage{templates: []Template{{Name: "15.1-RELEASE", Ready: false}}}
	_, _, err := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).
		Derive(context.Background(), "postgres-16", "15.1-RELEASE", nil)
	if err == nil {
		t.Fatal("Derive() returned no error")
	}
	if code := appErrCode(t, err); code != http.StatusConflict {
		t.Errorf("error code = %d, want 409", code)
	}
}

// No packages is a plain copy, which is a reasonable thing to want and must
// not run pkg at all.
func TestDeriveWithNoPackages(t *testing.T) {
	storage, packages := release(), &stubPackages{}
	template, built, err := NewService(storage, packages, &stubReleases{}, &fakeLogger{}).
		Derive(context.Background(), "snapshot-of-base", "15.1-RELEASE", nil)
	if err != nil || !built || !template.Ready {
		t.Fatalf("Derive() = %+v, %v, %v", template, built, err)
	}
	if len(packages.installed) != 0 {
		t.Errorf("ran pkg %q with no packages to install", packages.installed)
	}
}

// pkg ran and refused: a package that does not exist is the request's fault,
// and a client told 500 would retry it forever (JD-023's rule, same reasoning).
func TestAPackageThatDoesNotExistIsAClientError(t *testing.T) {
	storage := release()
	packages := &stubPackages{err: commandError("exit status 70",
		"pkg: No packages available to install matching 'nope' have been found", 70)}

	_, _, err := NewService(storage, packages, &stubReleases{}, &fakeLogger{}).
		Derive(context.Background(), "x", "15.1-RELEASE", []string{"nope"})
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Errorf("error code = %d, want 400 for a package pkg refused", code)
	}
	if !strings.Contains(err.Error(), "No packages available") {
		t.Errorf("error = %q, want pkg's own sentence in it", err)
	}
}

// commandError is what the real runner returns for a command that ran and
// failed.
func commandError(message, stderr string, exitCode int) error {
	return &system.CommandError{
		Command: "pkg",
		Result:  system.CommandResult{Stderr: stderr, ExitCode: exitCode},
		Err:     errors.New(message + ": " + stderr),
	}
}

func (s *stubStorage) Dependents(_ context.Context, name string) (Dependents, error) {
	return s.dependents[name], s.dependentsErr
}

func (s *stubStorage) Destroy(_ context.Context, name string) error {
	s.destroyed = append(s.destroyed, name)
	if s.destroyErr != nil {
		return s.destroyErr
	}
	s.templates = slices.DeleteFunc(s.templates, func(t Template) bool { return t.Name == name })
	return nil
}

func removable() *stubStorage {
	s := release()
	s.templates = append(s.templates, Template{
		Name: "postgres-16", Dataset: "tank/jails/templates/postgres-16", Ready: true,
		Snapshot: "tank/jails/templates/postgres-16@base", Parent: "15.1-RELEASE",
	})
	s.dependents = map[string]Dependents{}
	return s
}

func TestRemoveDestroysATemplateNothingDependsOn(t *testing.T) {
	storage := removable()
	removal, err := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).
		Remove(context.Background(), "postgres-16", "postgres-16")
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if !removal.Destroyed || removal.Blocked != nil {
		t.Errorf("removal = %+v, want it destroyed", removal)
	}
	if !reflect.DeepEqual(storage.destroyed, []string{"postgres-16"}) {
		t.Errorf("destroyed %q", storage.destroyed)
	}
	if _, ok := byName(storage.templates, "postgres-16"); ok {
		t.Error("the template is still in the listing")
	}
}

// The refusal the roadmap asks for, and the reason the check is made before
// the destroy rather than read out of zfs's complaint afterwards: this names
// the jails, and zfs's message names only the clones.
func TestRemoveRefusesWhileSomethingIsClonedFromIt(t *testing.T) {
	tests := []struct {
		name       string
		dependents Dependents
		wantIn     string
	}{
		{
			name:       "a jail cloned from it",
			dependents: Dependents{Clones: []string{"tank/jails/containers/pg-1"}},
			wantIn:     "pg-1",
		},
		{
			name:       "a template derived from it",
			dependents: Dependents{Clones: []string{"tank/jails/templates/postgres-16-tuned"}},
			wantIn:     "postgres-16-tuned",
		},
		{
			name:       "a hold on one of its snapshots",
			dependents: Dependents{Holds: []string{"tank/jails/templates/postgres-16@base (keep)"}},
			wantIn:     "keep",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := removable()
			storage.dependents["postgres-16"] = tt.dependents

			removal, err := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).
				Remove(context.Background(), "postgres-16", "postgres-16")
			if err != nil {
				t.Fatalf("Remove() error = %v", err)
			}
			if removal.Blocked == nil {
				t.Fatalf("removal = %+v, want it blocked", removal)
			}
			named := strings.Join(append(append([]string{}, removal.Blocked.Clones...),
				removal.Blocked.Holds...), " ")
			if !strings.Contains(named, tt.wantIn) {
				t.Errorf("blocked = %+v, want it to name %q", removal.Blocked, tt.wantIn)
			}
			// And nothing was destroyed.
			if len(storage.destroyed) != 0 {
				t.Errorf("destroyed %q while refusing", storage.destroyed)
			}
		})
	}
}

// A jail that should outlive its template is not stuck, and the refusal says
// so: zfs promote moves the dependency the other way.
func TestTheRefusalSaysHowToGetPastIt(t *testing.T) {
	storage := removable()
	storage.dependents["postgres-16"] = Dependents{Clones: []string{"tank/jails/containers/pg-1"}}

	removal, _ := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).
		Remove(context.Background(), "postgres-16", "postgres-16")
	if !strings.Contains(removal.Blocked.Reason, "promote") {
		t.Errorf("reason = %q, want it to mention zfs promote", removal.Blocked.Reason)
	}
}

func TestRemoveRefusals(t *testing.T) {
	tests := []struct {
		name     string
		template string
		confirm  string
		wantCode int
	}{
		{name: "no confirm", template: "postgres-16", confirm: "", wantCode: http.StatusBadRequest},
		{name: "confirm names another template", template: "postgres-16", confirm: "15.1-RELEASE", wantCode: http.StatusBadRequest},
		{name: "no such template", template: "absent", confirm: "absent", wantCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := removable()
			_, err := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).
				Remove(context.Background(), tt.template, tt.confirm)
			if err == nil {
				t.Fatal("Remove() returned no error")
			}
			if code := appErrCode(t, err); code != tt.wantCode {
				t.Errorf("error code = %d, want %d (%v)", code, tt.wantCode, err)
			}
			if len(storage.destroyed) != 0 {
				t.Errorf("destroyed %q for a refused call", storage.destroyed)
			}
		})
	}
}

// The half-built template a failed derive leaves behind is exactly what this
// endpoint exists to clean up, and nothing is cloned from it.
func TestRemoveAnUnreadyTemplate(t *testing.T) {
	storage := removable()
	storage.templates = append(storage.templates, Template{
		Name: "broken", Dataset: "tank/jails/templates/broken", Ready: false,
	})
	removal, err := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).
		Remove(context.Background(), "broken", "broken")
	if err != nil || !removal.Destroyed {
		t.Fatalf("Remove() = %+v, %v; want the half-built template destroyed", removal, err)
	}
}

// The detail endpoint's reason to exist: what depends on a template, visible
// before you try to remove it rather than only in the refusal.
func TestGetReportsWhatDependsOnATemplate(t *testing.T) {
	storage := removable()
	storage.dependents["postgres-16"] = Dependents{
		Clones: []string{"tank/jails/containers/pg-1", "tank/jails/templates/postgres-16-tuned"},
	}

	detail, err := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).
		Get(context.Background(), "postgres-16")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Name != "postgres-16" || !detail.Ready || detail.Parent != "15.1-RELEASE" {
		t.Errorf("detail = %+v, want the listing's fields", detail.Template)
	}
	if len(detail.Dependents.Clones) != 2 {
		t.Errorf("clones = %q, want both the jail and the derived template", detail.Dependents.Clones)
	}

	// The same answer Remove would refuse with, asked without trying.
	removal, _ := NewService(storage, &stubPackages{}, &stubReleases{}, &fakeLogger{}).
		Remove(context.Background(), "postgres-16", "postgres-16")
	if removal.Blocked == nil || len(removal.Blocked.Clones) != len(detail.Dependents.Clones) {
		t.Error("the detail and the refusal disagree about what depends on it")
	}
}

// Empty rather than null, so a client rendering the list does not have to
// check for it first.
func TestGetNeverReturnsNullDependents(t *testing.T) {
	detail, err := NewService(removable(), &stubPackages{}, &stubReleases{}, &fakeLogger{}).
		Get(context.Background(), "postgres-16")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Dependents.Clones == nil || detail.Dependents.Holds == nil {
		t.Errorf("dependents = %+v, want empty lists rather than nil", detail.Dependents)
	}
}

func TestGetAnAbsentTemplate(t *testing.T) {
	_, err := NewService(removable(), &stubPackages{}, &stubReleases{}, &fakeLogger{}).
		Get(context.Background(), "nope")
	if err == nil {
		t.Fatal("Get() returned no error")
	}
	if code := appErrCode(t, err); code != http.StatusNotFound {
		t.Errorf("error code = %d, want 404", code)
	}
}
