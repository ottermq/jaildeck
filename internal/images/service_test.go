package images

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
)

// stubSystem is an image store the test scripts: what is there, which stage
// fails, and a gate that holds a stage open so the in-flight state can be
// seen.
type stubSystem struct {
	mu      sync.Mutex
	store   Store
	images  map[string]Image
	fetched map[string]bool
	fail    map[string]error
	// gate, when set, holds Fetch and Write until it is closed.
	gate chan struct{}
	// listed, when set, holds the first two listings until both have
	// arrived, so two prepares can both find the image absent.
	listed  *sync.WaitGroup
	waiting int
	calls   []string
}

func newStub() *stubSystem {
	return &stubSystem{
		store:   Store{VMBhyve: VMBhyveReady, Dataset: "tank/vm/.images", Path: "/vm/.images"},
		images:  map[string]Image{},
		fetched: map[string]bool{},
		fail:    map[string]error{},
	}
}

func (s *stubSystem) record(call string) error {
	s.mu.Lock()
	s.calls = append(s.calls, call)
	gate, err := s.gate, s.fail[strings.Fields(call)[0]]
	s.mu.Unlock()
	if gate != nil && (strings.HasPrefix(call, "fetch") || strings.HasPrefix(call, "write")) {
		<-gate
	}
	return err
}

func (s *stubSystem) Store(context.Context) (Store, error) { return s.store, nil }

func (s *stubSystem) Sources() []Source {
	return []Source{{Name: "debian-13", Description: "Debian 13", URL: "https://example.invalid/debian-13.tar.xz"}}
}

func (s *stubSystem) Images(context.Context, Store) ([]Image, error) {
	s.mu.Lock()
	listed := s.listed
	if listed != nil {
		if s.waiting++; s.waiting == 2 {
			s.listed = nil
		}
	}
	s.mu.Unlock()
	if listed != nil {
		listed.Done()
		listed.Wait()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Image, 0, len(s.images))
	for _, image := range s.images {
		out = append(out, image)
	}
	return out, nil
}

func (s *stubSystem) Fetched(context.Context, Store) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for source := range s.fetched {
		out = append(out, source)
	}
	return out, nil
}

func (s *stubSystem) Fetch(_ context.Context, _ Store, source string) (string, error) {
	if err := s.record("fetch " + source); err != nil {
		return "fetch " + source, err
	}
	s.mu.Lock()
	s.fetched[source] = true
	s.mu.Unlock()
	return "fetch " + source, nil
}

func (s *stubSystem) Write(_ context.Context, store Store, name, source string) (string, error) {
	err := s.record("write " + name)
	s.mu.Lock()
	// The zvol exists whether or not tar then succeeds, as on a host.
	s.images[name] = Image{Name: name, Source: source, Dataset: store.Dataset + "/" + name, Guests: []string{}}
	s.mu.Unlock()
	return "write " + name, err
}

func (s *stubSystem) Seal(_ context.Context, store Store, name string) (string, error) {
	if err := s.record("seal " + name); err != nil {
		return "seal " + name, err
	}
	s.mu.Lock()
	image := s.images[name]
	image.Ready, image.Snapshot = true, store.Dataset+"/"+name+"@base"
	s.images[name] = image
	s.mu.Unlock()
	return "seal " + name, nil
}

func (s *stubSystem) Destroy(_ context.Context, _ Store, name string) (string, error) {
	if err := s.record("destroy " + name); err != nil {
		return "destroy " + name, err
	}
	s.mu.Lock()
	delete(s.images, name)
	s.mu.Unlock()
	return "destroy " + name, nil
}

type auditRecorder struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *auditRecorder) Log(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *auditRecorder) summary() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.entries))
	for _, e := range a.entries {
		ok := "ok"
		if !e.Success {
			ok = "failed"
		}
		out = append(out, e.Operation+" "+e.Command+" "+ok)
	}
	return out
}

// settled waits for the image's detached preparation to finish.
func settled(t *testing.T, s *Service, name string) Image {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, busy := s.running.of(name); !busy {
			image, err := s.Get(context.Background(), name)
			if err != nil {
				t.Fatalf("Get after preparing: %v", err)
			}
			return image
		}
		if time.Now().After(deadline) {
			t.Fatal("the preparation never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func wantCode(t *testing.T, err error, code int, contains string) {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != code || !strings.Contains(appErr.Message, contains) {
		t.Errorf("err = %v, want %d containing %q", err, code, contains)
	}
}

// --- preparing ---------------------------------------------------------------

func TestPrepareRunsEveryStageAndAuditsEach(t *testing.T) {
	stub, recorder := newStub(), &auditRecorder{}
	s := NewService(stub, recorder)

	image, started, err := s.Prepare(context.Background(), "debian-13", "")
	if err != nil || !started {
		t.Fatalf("Prepare = %+v, %v, %v", image, started, err)
	}
	if done := settled(t, s, "debian-13"); done.State != StateReady || done.Source != "debian-13" {
		t.Errorf("image = %+v, want ready from debian-13", done)
	}
	want := []string{"fetch-image fetch debian-13 ok", "write-image write debian-13 ok", "seal-image seal debian-13 ok"}
	if got := recorder.summary(); !slices.Equal(got, want) {
		t.Errorf("audit = %v, want %v", got, want)
	}
}

func TestPrepareAnswersWhileTheWorkIsInFlight(t *testing.T) {
	stub := newStub()
	stub.gate = make(chan struct{})
	s := NewService(stub, nil)

	image, started, err := s.Prepare(context.Background(), "trixie", "debian-13")
	if err != nil || !started {
		t.Fatalf("Prepare: %v, started %v", err, started)
	}
	// No zvol yet, and the image is there to be watched (JD-008).
	if image.State != StateFetching || image.Running == nil || image.Source != "debian-13" {
		t.Errorf("first answer = %+v, want fetching with its source", image)
	}
	_, _, err = s.Prepare(context.Background(), "trixie", "debian-13")
	wantCode(t, err, http.StatusConflict, "already being prepared")
	_, err = s.Delete(context.Background(), "trixie", "trixie")
	wantCode(t, err, http.StatusConflict, "being prepared right now")

	close(stub.gate)
	if done := settled(t, s, "trixie"); done.State != StateReady {
		t.Errorf("image = %+v, want ready", done)
	}
}

// Two prepares that both find the image absent: the claim, not the listing,
// is what lets only one of them start.
func TestTwoPreparesTogetherStartOne(t *testing.T) {
	stub := newStub()
	stub.gate = make(chan struct{})
	stub.listed = &sync.WaitGroup{}
	stub.listed.Add(2)
	s := NewService(stub, nil)

	started := make(chan bool, 2)
	for range 2 {
		go func() {
			_, ok, err := s.Prepare(context.Background(), "debian-13", "")
			if err != nil {
				wantCode(t, err, http.StatusConflict, "already being prepared")
			}
			started <- ok
		}()
	}
	if first, second := <-started, <-started; first == second {
		t.Errorf("started %v and %v, want exactly one", first, second)
	}
	close(stub.gate)
	settled(t, s, "debian-13")
	if fetches := slices.DeleteFunc(slices.Clone(stub.calls), func(c string) bool { return !strings.HasPrefix(c, "fetch") }); len(fetches) != 1 {
		t.Errorf("fetched %d times, want once", len(fetches))
	}
}

func TestPrepareStartsAtTheWriteWhenTheArchiveIsThere(t *testing.T) {
	stub, recorder := newStub(), &auditRecorder{}
	stub.fetched["debian-13"] = true
	stub.gate = make(chan struct{})
	s := NewService(stub, recorder)

	image, _, err := s.Prepare(context.Background(), "debian-13", "")
	if err != nil {
		t.Fatal(err)
	}
	if image.State != StateWriting {
		t.Errorf("state = %q, want writing", image.State)
	}
	close(stub.gate)
	settled(t, s, "debian-13")
	if got := recorder.summary(); len(got) != 2 || !strings.HasPrefix(got[0], "write-image") {
		t.Errorf("audit = %v, want no fetch", got)
	}
}

func TestAWriteThatFailsLeavesAnImageThatIsNotReady(t *testing.T) {
	stub, recorder := newStub(), &auditRecorder{}
	stub.fail["write"] = errors.New("tar: Error opening archive")
	s := NewService(stub, recorder)

	if _, _, err := s.Prepare(context.Background(), "debian-13", ""); err != nil {
		t.Fatal(err)
	}
	if image := settled(t, s, "debian-13"); image.State != StateWritten || image.Ready {
		t.Errorf("image = %+v, want written and not ready", image)
	}
	want := []string{"fetch-image fetch debian-13 ok", "write-image write debian-13 failed"}
	if got := recorder.summary(); !slices.Equal(got, want) {
		t.Errorf("audit = %v, want %v (and no seal)", got, want)
	}

	// Written over, it would be a disk nothing can vouch for.
	_, _, err := s.Prepare(context.Background(), "debian-13", "")
	wantCode(t, err, http.StatusConflict, "remove it and prepare it again")
}

func TestPrepareIsIdempotentForAReadyImage(t *testing.T) {
	stub := newStub()
	stub.images["debian-13"] = Image{Name: "debian-13", Source: "debian-13", Ready: true, Guests: []string{}}
	s := NewService(stub, nil)

	image, started, err := s.Prepare(context.Background(), "debian-13", "debian-13")
	if err != nil || started || image.State != StateReady {
		t.Errorf("Prepare = %+v, started %v, %v; want the ready image and nothing started", image, started, err)
	}
	if len(stub.calls) != 0 {
		t.Errorf("ran %v", stub.calls)
	}
}

func TestPrepareRefuses(t *testing.T) {
	cases := []struct {
		name, image, source string
		setup               func(*stubSystem)
		code                int
		contains            string
	}{
		{"a bad name", "-debian", "debian-13", nil, http.StatusBadRequest, "not an image name"},
		{"a dot name", ".images", "debian-13", nil, http.StatusBadRequest, "not an image name"},
		{"an unknown source", "ubuntu", "ubuntu-24.04", nil, http.StatusBadRequest, "it can prepare debian-13"},
		{"no source, and a name that is none", "mine", "", nil, http.StatusBadRequest, "mine is not a source, so say which one"},
		{"vm-bhyve not configured", "debian-13", "", func(s *stubSystem) {
			s.store = Store{VMBhyve: VMBhyveNotConfigured, Problem: "$vm_enable is not enabled in /etc/rc.conf!"}
		}, http.StatusConflict, "$vm_enable is not enabled"},
		{"a datastore not on ZFS", "debian-13", "", func(s *stubSystem) {
			s.store = Store{VMBhyve: VMBhyveReady, Problem: "not on ZFS"}
		}, http.StatusConflict, "not on ZFS"},
		{"another source's image", "base", "debian-13", func(s *stubSystem) {
			s.images["base"] = Image{Name: "base", Source: "debian-12", Ready: true}
		}, http.StatusConflict, "written from debian-12, not debian-13"},
		{"a zvol Jail Deck did not write", "base", "debian-13", func(s *stubSystem) {
			s.images["base"] = Image{Name: "base", Ready: true}
		}, http.StatusConflict, "written from no source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStub()
			if tc.setup != nil {
				tc.setup(stub)
			}
			_, _, err := NewService(stub, nil).Prepare(context.Background(), tc.image, tc.source)
			wantCode(t, err, tc.code, tc.contains)
			if len(stub.calls) != 0 {
				t.Errorf("ran %v", stub.calls)
			}
		})
	}
}

// --- listing -----------------------------------------------------------------

func TestListSaysWhyThereAreNoImages(t *testing.T) {
	stub := newStub()
	stub.store = Store{VMBhyve: VMBhyveNotInstalled, Problem: "vm-bhyve is not installed"}
	listing, err := NewService(stub, nil).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if listing.VMBhyve != VMBhyveNotInstalled || listing.Problem == "" || listing.Images == nil ||
		len(listing.Images) != 0 || len(listing.Sources) != 1 || listing.Sources[0].Fetched {
		t.Errorf("listing = %+v", listing)
	}
}

func TestListMarksFetchedSourcesAndSortsImages(t *testing.T) {
	stub := newStub()
	stub.fetched["debian-13"] = true
	stub.images["zeta"] = Image{Name: "zeta", Ready: true, Guests: []string{}}
	stub.images["alpha"] = Image{Name: "alpha", Guests: []string{}}
	listing, err := NewService(stub, nil).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !listing.Sources[0].Fetched {
		t.Error("debian-13's archive is there and the source does not say so")
	}
	if len(listing.Images) != 2 || listing.Images[0].Name != "alpha" || listing.Images[0].State != StateWritten ||
		listing.Images[1].State != StateReady {
		t.Errorf("images = %+v", listing.Images)
	}
}

// --- removing ----------------------------------------------------------------

func TestDeleteRefusesWhileAGuestUsesIt(t *testing.T) {
	stub, recorder := newStub(), &auditRecorder{}
	stub.images["debian-13"] = Image{Name: "debian-13", Ready: true, Guests: []string{"web", "db"}}
	s := NewService(stub, recorder)

	_, err := s.Delete(context.Background(), "debian-13", "debian-13")
	wantCode(t, err, http.StatusConflict, "web, db")
	if len(stub.calls) != 0 || len(recorder.entries) != 0 {
		t.Errorf("ran %v, audited %v", stub.calls, recorder.summary())
	}
}

func TestDeleteNeedsConfirmAndAnImage(t *testing.T) {
	stub := newStub()
	stub.images["debian-13"] = Image{Name: "debian-13", Ready: true, Guests: []string{}}
	s := NewService(stub, nil)

	_, err := s.Delete(context.Background(), "debian-13", "")
	wantCode(t, err, http.StatusBadRequest, "confirm=debian-13")
	_, err = s.Delete(context.Background(), "nothing", "nothing")
	wantCode(t, err, http.StatusNotFound, "no image")
}

func TestDeleteDestroysAndAudits(t *testing.T) {
	stub, recorder := newStub(), &auditRecorder{}
	stub.images["debian-13"] = Image{Name: "debian-13", Dataset: "tank/vm/.images/debian-13", Guests: []string{}}
	s := NewService(stub, recorder)

	removal, err := s.Delete(context.Background(), "debian-13", "debian-13")
	if err != nil || removal != (Removal{Name: "debian-13", Dataset: "tank/vm/.images/debian-13"}) {
		t.Fatalf("Delete = %+v, %v", removal, err)
	}
	if got := recorder.summary(); !slices.Equal(got, []string{"destroy-image destroy debian-13 ok"}) {
		t.Errorf("audit = %v", got)
	}
}

func TestADestroyThatFailsIsAuditedAndSaid(t *testing.T) {
	stub, recorder := newStub(), &auditRecorder{}
	stub.images["debian-13"] = Image{Name: "debian-13", Guests: []string{}}
	stub.fail["destroy"] = errors.New("dataset is busy")
	s := NewService(stub, recorder)

	_, err := s.Delete(context.Background(), "debian-13", "debian-13")
	wantCode(t, err, http.StatusInternalServerError, "dataset is busy")
	if got := recorder.summary(); !slices.Equal(got, []string{"destroy-image destroy debian-13 failed"}) {
		t.Errorf("audit = %v", got)
	}
}
