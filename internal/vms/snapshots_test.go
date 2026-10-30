package vms

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

// stubSnapshots answers as the storage domain does, and records the datasets
// it was asked to snapshot.
type stubSnapshots struct {
	existed bool
	asked   []string
}

func (s *stubSnapshots) SnapshotDataset(_ context.Context, dataset, short string) (SnapshotResult, error) {
	s.asked = append(s.asked, dataset+"@"+short)
	if s.existed {
		return SnapshotResult{Snapshot: dataset + "@" + short, Created: []string{}, Existed: true}, nil
	}
	return SnapshotResult{Snapshot: dataset + "@" + short,
		Created: []string{dataset + "@" + short, dataset + "/disk0@" + short}}, nil
}

func snapshotHost() (*stubSnapshots, *Service) {
	_, _, s := labelledHost()
	taker := &stubSnapshots{}
	s.SetSnapshots(taker)
	return taker, s
}

// A running guest is snapshotted as it is, and the answer says what that is
// worth; a stopped one's is consistent (JD-045).
func TestCreateSnapshotSaysHowConsistent(t *testing.T) {
	for _, tt := range []struct {
		guest string
		want  Consistency
	}{
		{guest: "web", want: CrashConsistent},
		{guest: "db", want: Consistent},
	} {
		t.Run(tt.guest, func(t *testing.T) {
			taker, s := snapshotHost()
			got, err := s.CreateSnapshot(context.Background(), tt.guest, "before")
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"tank/vm/" + tt.guest + "@before"}; !reflect.DeepEqual(taker.asked, want) {
				t.Errorf("asked %q, want %q", taker.asked, want)
			}
			if got.Consistency != tt.want || len(got.Created) != 2 {
				t.Errorf("result %+v, want %s with the disk", got, tt.want)
			}
		})
	}
}

// A snapshot already there was taken some other time, in a state nobody
// recorded: no consistency is claimed for it.
func TestCreateSnapshotAlreadyThere(t *testing.T) {
	taker, s := snapshotHost()
	taker.existed = true
	got, err := s.CreateSnapshot(context.Background(), "web", "before")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Existed || got.Consistency != "" {
		t.Errorf("result %+v, want existed and no consistency", got)
	}
}

func TestCreateSnapshotRefusals(t *testing.T) {
	for _, tt := range []struct {
		name, guest string
		want        int
		says        string
	}{
		{name: "no such guest", guest: "nope", want: http.StatusNotFound},
		{name: "not a guest name", guest: "-bad", want: http.StatusBadRequest},
		{name: "a directory datastore", guest: "old", want: http.StatusConflict, says: "not on ZFS"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			taker, s := snapshotHost()
			_, err := s.CreateSnapshot(context.Background(), tt.guest, "before")
			wantAppError(t, err, tt.want, tt.says)
			if len(taker.asked) != 0 {
				t.Errorf("asked %q, want nothing", taker.asked)
			}
		})
	}
	t.Run("without a store", func(t *testing.T) {
		_, _, s := labelledHost()
		_, err := s.CreateSnapshot(context.Background(), "web", "before")
		wantAppError(t, err, http.StatusNotImplemented, "storage access")
	})
}
