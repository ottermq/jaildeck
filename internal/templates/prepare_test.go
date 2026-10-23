package templates

import (
	"context"
	"errors"
	"testing"
	"time"
)

// blockingReleases holds FetchBase open until it is released, so the in-flight
// state a real fetch spends minutes in can be observed in a test.
type blockingReleases struct {
	stubReleases
	started chan struct{}
	release chan struct{}
}

func newBlockingReleases() *blockingReleases {
	return &blockingReleases{started: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingReleases) FetchBase(ctx context.Context, release, arch string) (Result, error) {
	close(b.started)
	<-b.release
	return b.stubReleases.FetchBase(ctx, release, arch)
}

// The shape of JD-008 in one test: the call returns before the work does, the
// template exists from that moment, and its state comes from what is running
// rather than from the disk, which still shows nothing.
func TestPrepareReturnsBeforeTheWorkFinishes(t *testing.T) {
	releases := newBlockingReleases()
	service := NewService(&stubStorage{}, &stubPackages{}, releases, &fakeLogger{})

	template, err := service.Prepare(context.Background(), "15.1-RELEASE", "15.1-RELEASE")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	waitFor(t, releases.started, "FetchBase to start")

	if template.State != StateFetching {
		t.Errorf("state = %q, want %q", template.State, StateFetching)
	}
	if template.Dataset != "" {
		t.Errorf("dataset = %q, want none: nothing has been created yet", template.Dataset)
	}

	// The same answer a client polling the resource gets, which is the whole
	// mechanism: there is no job to ask about, only the template.
	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	found, ok := byName(listed, "15.1-RELEASE")
	if !ok {
		t.Fatal("the template being prepared is not in the listing")
	}
	if found.Running == nil {
		t.Fatal("running = nil, want the step in flight")
	}
	if found.Running.Since.IsZero() {
		t.Error("running.since is zero; a client cannot tell how long it has been going")
	}

	close(releases.release)
	waitUntil(t, func() bool {
		after, _ := service.List(context.Background())
		found, _ := byName(after, "15.1-RELEASE")
		return found.State == StateFetched && found.Running == nil
	}, "the template to reach fetched with nothing running")
}

// A second request while one is in flight is refused rather than queued: two
// fetches writing the same file is not a thing to serialise.
func TestPrepareRefusesWhileOneIsRunning(t *testing.T) {
	releases := newBlockingReleases()
	service := NewService(&stubStorage{}, &stubPackages{}, releases, &fakeLogger{})

	if _, err := service.Prepare(context.Background(), "15.1-RELEASE", "15.1-RELEASE"); err != nil {
		t.Fatalf("first Prepare() error = %v", err)
	}
	waitFor(t, releases.started, "FetchBase to start")

	_, err := service.Prepare(context.Background(), "15.1-RELEASE", "15.1-RELEASE")
	if err == nil {
		t.Fatal("second Prepare() succeeded, want a refusal while one is in flight")
	}
	// The refusal happened before the first fetch had even recorded itself,
	// which is the point: the claim is the check, so two requests arriving
	// together cannot both get through.
	close(releases.release)
	waitUntil(t, func() bool { return len(releases.tried()) == 1 },
		"the one fetch that was allowed to start")

	if got := releases.tried(); len(got) != 1 {
		t.Errorf("fetched %v, want exactly one attempt", got)
	}
}

// Nothing is retried and nothing is cleaned up: the next read of the
// filesystem reports the stage that did complete, and the audit log says why
// the next one did not (JD-008).
func TestPrepareLeavesAFailureWhereItStopped(t *testing.T) {
	logger := &fakeLogger{}
	releases := &stubReleases{fetchErr: errors.New("fetch: Not Found")}
	service := NewService(&stubStorage{}, &stubPackages{}, releases, logger)

	if _, err := service.Prepare(context.Background(), "15.0-RELEASE", "15.0-RELEASE"); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	waitUntil(t, func() bool {
		listed, _ := service.List(context.Background())
		found, ok := byName(listed, "15.0-RELEASE")
		return !ok || (found.State == StateAbsent && found.Running == nil)
	}, "the failed prepare to stop running and report absent")

	if len(logger.entries) != 1 {
		t.Fatalf("logged %d entries, want the one failed stage", len(logger.entries))
	}
	if entry := logger.entries[0]; entry.Success || entry.Operation != OperationFetchBase {
		t.Errorf("entry = %+v, want a failed %s", entry, OperationFetchBase)
	}
}

// A release that is already ready is not work to start again. Same idempotent
// answer Derive gives: a client that timed out and repeated has not erred.
func TestPrepareOnAReadyTemplateDoesNothing(t *testing.T) {
	releases := &stubReleases{}
	storage := &stubStorage{templates: []Template{
		{Name: "15.1-RELEASE", Dataset: "tank/jails/templates/15.1-RELEASE", Ready: true},
	}}
	service := NewService(storage, &stubPackages{}, releases, &fakeLogger{})

	template, err := service.Prepare(context.Background(), "15.1-RELEASE", "15.1-RELEASE")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if template.State != StateReady {
		t.Errorf("state = %q, want %q", template.State, StateReady)
	}
	if len(releases.tried()) != 0 {
		t.Errorf("fetched %v, want nothing: it is already usable", releases.tried())
	}
}

// The mirror publishes no patch-level directory, so a -pN release is a 404
// waiting to happen. Refused here, by name, rather than attempted.
func TestPrepareRefusesAPatchLevel(t *testing.T) {
	service := NewService(&stubStorage{}, &stubPackages{}, &stubReleases{}, &fakeLogger{})

	for _, release := range []string{"15.1-RELEASE-p5", "15.1", "", "../etc"} {
		if release == "" {
			continue // empty means "the host's own", which is valid
		}
		if _, err := service.Prepare(context.Background(), "t", release); err == nil {
			t.Errorf("Prepare(%q) succeeded, want a refusal", release)
		}
	}
}

// An empty release is the host's own, which is also the ceiling: a jail's
// userland must be the same as or older than the kernel it runs under.
func TestPrepareDefaultsToTheHostRelease(t *testing.T) {
	releases := &stubReleases{host: Host{Release: "14.3-RELEASE", Arch: "amd64"}}
	service := NewService(&stubStorage{}, &stubPackages{}, releases, &fakeLogger{})

	if _, err := service.Prepare(context.Background(), "14.3-RELEASE", ""); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	waitUntil(t, func() bool { return len(releases.tried()) == 1 }, "the fetch to run")

	if releases.tried()[0] != "14.3-RELEASE" {
		t.Errorf("fetched %q, want the host's own release", releases.tried()[0])
	}
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitUntil(t *testing.T, done func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
