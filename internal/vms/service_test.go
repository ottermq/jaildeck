package vms

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

type stubSystem struct {
	noCreation
	listing Listing
	listErr error
	info    map[string]Info
	infoErr error
	calls   []string
}

func (s *stubSystem) VMList(context.Context) (Listing, error) {
	s.calls = append(s.calls, "list")
	return s.listing, s.listErr
}

func (s *stubSystem) VMStart(context.Context, string) (string, error) {
	panic("the read-only tests start nothing")
}

func (s *stubSystem) VMStop(context.Context, string) (string, error) {
	panic("the read-only tests stop nothing")
}

func (s *stubSystem) VMPoweroff(context.Context, string) (string, error) {
	panic("the read-only tests power nothing off")
}

func (s *stubSystem) VMInfo(_ context.Context, name string) (Info, error) {
	s.calls = append(s.calls, "info "+name)
	if s.infoErr != nil {
		return Info{}, s.infoErr
	}
	return s.info[name], nil
}

func ready(names ...string) Listing {
	listing := Listing{VMBhyve: VMBhyveReady}
	for _, name := range names {
		listing.VMs = append(listing.VMs, VM{Name: name, State: StateStopped})
	}
	return listing
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("err = %v, want an AppError", err)
	}
	return appErr.Code
}

func TestListKeepsAnEmptyListAList(t *testing.T) {
	s := NewService(&stubSystem{listing: Listing{VMBhyve: VMBhyveReady}}, nil, nil)
	listing, err := s.List(context.Background())
	if err != nil || listing.VMs == nil || listing.Switches == nil {
		t.Errorf("listing = %+v, err = %v; want empty lists, not null", listing, err)
	}
}

func TestListNamesTheSwitchesWhenVMBhyveIsReady(t *testing.T) {
	m := newMaker()
	listing, err := NewService(m, nil, nil).List(context.Background())
	if err != nil || !slices.Equal(listing.Switches, []string{"jd-capsw"}) {
		t.Errorf("switches = %v, err = %v", listing.Switches, err)
	}
	// Not ready: not asked.
	m.listing = Listing{VMBhyve: VMBhyveNotInstalled}
	m.fail["switches"] = errors.New("must not be asked")
	if listing, err := NewService(m, nil, nil).List(context.Background()); err != nil || len(listing.Switches) != 0 {
		t.Errorf("switches = %v, err = %v", listing.Switches, err)
	}
	// Ready, and the switches cannot be read: the guests are still listed.
	m.listing = Listing{VMBhyve: VMBhyveReady, VMs: []VM{{Name: "web"}}}
	if listing, err := NewService(m, nil, nil).List(context.Background()); err != nil || len(listing.VMs) != 1 {
		t.Errorf("listing = %+v, err = %v; want the guests anyway", listing, err)
	}
}

// Not configured is a 200 that says so, with vm-bhyve's words.
func TestListPassesOnVMBhyvesState(t *testing.T) {
	want := Listing{VMBhyve: VMBhyveNotConfigured, Problem: "$vm_enable is not enabled in /etc/rc.conf!",
		VMs: []VM{}}
	s := NewService(&stubSystem{listing: want}, nil, nil)
	listing, err := s.List(context.Background())
	if err != nil || listing.VMBhyve != want.VMBhyve || listing.Problem != want.Problem {
		t.Errorf("listing = %+v, err = %v; want %+v", listing, err, want)
	}
}

func TestListReadErrorIs500(t *testing.T) {
	s := NewService(&stubSystem{listErr: errors.New("vm: killed")}, nil, nil)
	_, err := s.List(context.Background())
	if statusOf(t, err) != http.StatusInternalServerError {
		t.Errorf("err = %v, want 500", err)
	}
}

func TestWithoutHostAccessIs501(t *testing.T) {
	s := NewService(nil, nil, nil)
	if _, err := s.List(context.Background()); statusOf(t, err) != http.StatusNotImplemented {
		t.Errorf("List: %v, want 501", err)
	}
	if _, err := s.Get(context.Background(), "web"); statusOf(t, err) != http.StatusNotImplemented {
		t.Errorf("Get: %v, want 501", err)
	}
}

// The detail is the listing's row with vm info's additions: autostart comes
// from the row, because vm info does not say.
func TestGetJoinsTheRowAndTheInfo(t *testing.T) {
	listing := ready("web")
	listing.VMs[0].Autostart, listing.VMs[0].AutostartOrder = true, 1
	system := &stubSystem{listing: listing, info: map[string]Info{"web": {UUID: "abc"}}}
	detail, err := NewService(system, nil, nil).Get(context.Background(), "web")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if detail.Name != "web" || !detail.Autostart || detail.UUID != "abc" {
		t.Errorf("detail = %+v, want web, autostart, uuid abc", detail)
	}
	if detail.Networks == nil || detail.Disks == nil {
		t.Errorf("detail = %+v, want empty lists, not null", detail)
	}
}

// vm-bhyve's own name rule decides a 400 before anything is asked.
func TestGetRefusesWhatCannotBeAGuestName(t *testing.T) {
	for _, name := range []string{"", "a", "-web", "web-", "we b", "web/../x", "web;ls", "../etc"} {
		system := &stubSystem{listing: ready("web")}
		_, err := NewService(system, nil, nil).Get(context.Background(), name)
		if statusOf(t, err) != http.StatusBadRequest {
			t.Errorf("%q: err = %v, want 400", name, err)
		}
		if len(system.calls) != 0 {
			t.Errorf("%q: asked vm-bhyve %v before refusing", name, system.calls)
		}
	}
}

// A name not in the listing is a 404 decided by Jail Deck: vm info is never
// asked about it.
func TestGetAnUnlistedNameIs404WithoutAskingVMInfo(t *testing.T) {
	system := &stubSystem{listing: ready("web")}
	_, err := NewService(system, nil, nil).Get(context.Background(), "db")
	if statusOf(t, err) != http.StatusNotFound {
		t.Errorf("err = %v, want 404", err)
	}
	if len(system.calls) != 1 {
		t.Errorf("calls = %v, want the listing only", system.calls)
	}
}

// Destroyed between the listing and vm info.
func TestGetAGuestThatVanishesIs404(t *testing.T) {
	system := &stubSystem{listing: ready("web"), infoErr: ErrNoSuchVM}
	_, err := NewService(system, nil, nil).Get(context.Background(), "web")
	if statusOf(t, err) != http.StatusNotFound {
		t.Errorf("err = %v, want 404", err)
	}
}

func TestGetWhenVMBhyveCannotListIs409(t *testing.T) {
	for _, listing := range []Listing{
		{VMBhyve: VMBhyveNotInstalled},
		{VMBhyve: VMBhyveNotConfigured, Problem: "$vm_dir has not been configured"},
	} {
		_, err := NewService(&stubSystem{listing: listing}, nil, nil).Get(context.Background(), "web")
		if statusOf(t, err) != http.StatusConflict {
			t.Errorf("%s: err = %v, want 409", listing.VMBhyve, err)
		}
	}
}
