package jails

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

// listSystem is a JailSystem that actually has jails, which stubSystem does
// not: its List exists only to return an error.
type listSystem struct {
	jails []Jail
	err   error
}

func (s listSystem) List(context.Context) ([]Jail, error) { return s.jails, s.err }
func (s listSystem) Start(context.Context, string) (OperationResult, error) {
	return OperationResult{}, nil
}
func (s listSystem) Stop(context.Context, string) (OperationResult, error) {
	return OperationResult{}, nil
}
func (s listSystem) Restart(context.Context, string) (OperationResult, error) {
	return OperationResult{}, nil
}

type stubDatasets struct {
	storage map[string]JailStorage
	err     error
	asked   [][]string
}

func (s *stubDatasets) StorageForPaths(_ context.Context, paths []string) (map[string]JailStorage, error) {
	s.asked = append(s.asked, paths)
	return s.storage, s.err
}

func threeJails() listSystem {
	return listSystem{jails: []Jail{
		{Name: "ottermq", Status: JailStatusRunning, Path: "/usr/local/jails/containers/ottermq"},
		{Name: "postgres", Status: JailStatusStopped, Path: "/usr/local/jails/containers/postgres"},
		// A configured jail whose stanza sets no path.
		{Name: "pathless", Status: JailStatusStopped},
	}}
}

func ottermqStorage() *stubDatasets {
	return &stubDatasets{storage: map[string]JailStorage{
		"/usr/local/jails/containers/ottermq": {
			Datasets:   []Dataset{{Name: "tank/jails/containers/ottermq", Mountpoint: "/usr/local/jails/containers/ottermq", Used: 31850496, Referenced: 416907264, Available: 963201556480}},
			Properties: map[string]string{"otter:instance": "mq-01"},
		},
	}}
}

func TestListFillsInStorage(t *testing.T) {
	datasets := ottermqStorage()
	got, err := NewJailService(Dependencies{System: threeJails(), Audit: &fakeLogger{}, Datasets: datasets}).List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List() = %d jails, want 3", len(got))
	}

	if got[0].Properties["otter:instance"] != "mq-01" || len(got[0].Datasets) != 1 {
		t.Errorf("ottermq = %+v, want its dataset and its otter:instance", got[0])
	}
	// A jail the source did not answer for keeps both fields absent, which
	// omitempty turns into no key at all rather than a null.
	for _, jail := range got[1:] {
		if jail.Datasets != nil || jail.Properties != nil {
			t.Errorf("%s = %+v, want no datasets and no properties", jail.Name, jail)
		}
	}

	// One read for the whole listing, and the pathless jail is not in it: an
	// empty path would prefix-match every dataset on the host.
	want := [][]string{{"/usr/local/jails/containers/ottermq", "/usr/local/jails/containers/postgres"}}
	if !reflect.DeepEqual(datasets.asked, want) {
		t.Errorf("asked %v, want %v", datasets.asked, want)
	}
}

// An unreadable pool must not cost the operator the listing: seeing a stopped
// jail is how they start it (JD-004). The jails come back without storage and
// the reason is logged at WARN.
func TestListDegradesWhenStorageFails(t *testing.T) {
	buf := captureLogs(t)
	datasets := &stubDatasets{err: errors.New("zfs: pool is suspended")}

	got, err := NewJailService(Dependencies{System: threeJails(), Audit: &fakeLogger{}, Datasets: datasets}).List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v, want the listing to survive a storage failure", err)
	}
	if len(got) != 3 {
		t.Errorf("List() = %d jails, want all 3", len(got))
	}
	for _, jail := range got {
		if jail.Datasets != nil || jail.Properties != nil {
			t.Errorf("%s = %+v, want no storage after a failed read", jail.Name, jail)
		}
	}

	records := decodeLogs(t, buf)
	logged := false
	for _, rec := range records {
		if rec.Level == "WARN" && rec.Component == "jail" && rec.Err == "zfs: pool is suspended" {
			logged = true
		}
	}
	if !logged {
		t.Errorf("no WARN naming the failure in %+v", records)
	}
}

// No source wired at all: the listing still works, which is what lets every
// test that is not about storage pass nil.
func TestListWithoutADatasetSource(t *testing.T) {
	got, err := NewJailService(Dependencies{System: threeJails(), Audit: &fakeLogger{}}).List(context.Background())
	if err != nil || len(got) != 3 {
		t.Fatalf("List() = %d jails, %v; want 3, nil", len(got), err)
	}
}

func TestGet(t *testing.T) {
	tests := []struct {
		name string
		ask  string
		// wantCode is 0 when the jail must be found. A rejected name is a 400
		// and an absent one a 404; only the empty name is the shared
		// ErrInvalidJailName sentinel, because NewJailName builds a fresh error
		// naming the offending input for every other rejection.
		wantCode int
	}{
		{name: "a running jail", ask: "ottermq"},
		{name: "a stopped jail", ask: "postgres"},
		{name: "a jail with no path", ask: "pathless"},
		{name: "not on this host", ask: "absent", wantCode: http.StatusNotFound},
		{name: "not a jail name", ask: "../../etc/passwd", wantCode: http.StatusBadRequest},
		{name: "a name with a space", ask: "otter mq", wantCode: http.StatusBadRequest},
		{name: "empty", ask: "", wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewJailService(Dependencies{System: threeJails(), Audit: &fakeLogger{}, Datasets: ottermqStorage()})
			got, err := svc.Get(context.Background(), JailName(tt.ask))
			if tt.wantCode != 0 {
				var appErr *common.AppError
				if !errors.As(err, &appErr) {
					t.Fatalf("Get(%q) = %+v, %v; want an *common.AppError", tt.ask, got, err)
				}
				if appErr.Code != tt.wantCode {
					t.Errorf("Get(%q) error code = %d, want %d (%v)", tt.ask, appErr.Code, tt.wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get(%q) error = %v", tt.ask, err)
			}
			if got.Name != tt.ask {
				t.Errorf("Get(%q) returned %q", tt.ask, got.Name)
			}
		})
	}

	t.Run("carries its storage", func(t *testing.T) {
		datasets := ottermqStorage()
		got, err := NewJailService(Dependencies{System: threeJails(), Audit: &fakeLogger{}, Datasets: datasets}).Get(context.Background(), "ottermq")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got.Properties["otter:instance"] != "mq-01" || len(got.Datasets) != 1 {
			t.Errorf("Get() = %+v, want its dataset and its otter:instance", got)
		}
		// Scoped to the one jail asked about, not to the whole listing.
		want := [][]string{{"/usr/local/jails/containers/ottermq"}}
		if !reflect.DeepEqual(datasets.asked, want) {
			t.Errorf("asked %v, want %v", datasets.asked, want)
		}
	})

	t.Run("a failed listing is not a 404", func(t *testing.T) {
		svc := NewJailService(Dependencies{System: listSystem{err: errors.New("jls: Operation not permitted")}, Audit: &fakeLogger{}})
		_, err := svc.Get(context.Background(), "ottermq")
		if err == nil || errors.Is(err, ErrJailNotFound) {
			t.Errorf("Get() error = %v, want a failure rather than not-found", err)
		}
	})
}
