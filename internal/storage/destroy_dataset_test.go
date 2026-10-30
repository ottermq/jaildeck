package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

const retainedOld = "tank/jails/retained/old"

// A retained dataset with two snapshots, a sibling whose name starts the same
// way, and a jail with a child -- the shapes #120's capture measured.
func withDestroyable() *stubSystem {
	return &stubSystem{
		pools: []string{"tank"},
		datasets: []Dataset{
			{Name: "tank/jails", Mountpoint: "/usr/local/jails"},
			{Name: "tank/jails/retained", Mountpoint: "/usr/local/jails/retained"},
			{Name: retainedOld, Mountpoint: "/usr/local/jails/retained/old"},
			{Name: retainedOld + "-2", Mountpoint: "/usr/local/jails/retained/old-2"},
			{Name: "tank/jails/containers/ottermq", Mountpoint: "/usr/local/jails/containers/ottermq"},
			{Name: ottermqData, Mountpoint: "/usr/local/jails/containers/ottermq/data"},
		},
		snapshots: []Snapshot{
			{Name: retainedOld + "@one", Dataset: retainedOld, Short: "one", CreateTXG: 10},
			{Name: retainedOld + "@two", Dataset: retainedOld, Short: "two", CreateTXG: 20},
			{Name: retainedOld + "-2@one", Dataset: retainedOld + "-2", Short: "one", CreateTXG: 11},
		},
		clones:     map[string][]string{},
		notMounted: map[string]bool{},
	}
}

// One command, -r because it has snapshots, never -R, and the snapshots it
// took named in the answer: they go silently otherwise.
func TestDestroyRetainedTakesItsSnapshots(t *testing.T) {
	system := withDestroyable()
	logger := &fakeLogger{}

	got, err := NewService(system, logger, "tank").DestroyRetained(context.Background(), "old", "old")
	if err != nil {
		t.Fatalf("DestroyRetained() error = %v", err)
	}
	want0 := []string{"zfs unmount " + retainedOld, "zfs destroy -r " + retainedOld}
	if !reflect.DeepEqual(system.ran, want0) {
		t.Errorf("ran %q, want %q", system.ran, want0)
	}
	want := DatasetDestroy{
		Dataset:   retainedOld,
		Snapshots: []string{retainedOld + "@one", retainedOld + "@two"},
		Destroyed: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if len(logger.entries) != 2 || logger.entries[1].Operation != OperationDestroyDataset || !logger.entries[1].Success {
		t.Errorf("audit = %+v, want the unmount and then a successful %s", logger.entries, OperationDestroyDataset)
	}
}

// Without snapshots the destroy is bare, so one taken in the gap since they
// were read makes zfs refuse rather than -r taking it along unlisted.
func TestDestroyRetainedWithoutSnapshotsIsBare(t *testing.T) {
	system := withDestroyable()
	system.snapshots = nil
	// Not mounted, so not unmounted either: a command with nothing to do.
	system.notMounted[retainedOld] = true

	got, err := NewService(system, &fakeLogger{}, "tank").DestroyRetained(context.Background(), "old", "old")
	if err != nil {
		t.Fatalf("DestroyRetained() error = %v", err)
	}
	if want := []string{"zfs destroy " + retainedOld}; !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran %q, want %q", system.ran, want)
	}
	if got.Snapshots == nil || len(got.Snapshots) != 0 {
		t.Errorf("snapshots = %#v, want an empty list, not null", got.Snapshots)
	}
}

// Everything that refuses, refuses before zfs is asked to destroy anything.
func TestDestroyRetainedRefusals(t *testing.T) {
	tests := []struct {
		name     string
		retained string
		confirm  string
		wantCode int
		wantText string
	}{
		{name: "no confirm", retained: "old", wantCode: http.StatusBadRequest,
			wantText: "this destroys " + retainedOld + " and its snapshots; it needs confirm=old"},
		{name: "confirm of another", retained: "old", confirm: "old-2", wantCode: http.StatusBadRequest},
		{name: "the full path as confirm", retained: "old", confirm: retainedOld, wantCode: http.StatusBadRequest},
		{name: "already gone", retained: "gone", confirm: "gone", wantCode: http.StatusNotFound,
			wantText: "nothing retained under the name gone"},
		{name: "a path out of retained", retained: "../containers/ottermq", confirm: "../containers/ottermq",
			wantCode: http.StatusBadRequest},
		{name: "a nested name", retained: "old/inner", confirm: "old/inner", wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withDestroyable()
			_, err := NewService(system, &fakeLogger{}, "tank").DestroyRetained(context.Background(), tt.retained, tt.confirm)
			if code := appErrCode(t, err); code != tt.wantCode {
				t.Errorf("code = %d, want %d (%v)", code, tt.wantCode, err)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %q, want it to say %q", err, tt.wantText)
			}
			if len(system.ran) != 0 {
				t.Errorf("ran %q, want nothing", system.ran)
			}
		})
	}
}

// A hold, a clone and a dataset of its own each block on their own, and the
// answer names them. zfs refuses the first two itself, but -r takes the third
// without a word (measured, #120), so that one is Jail Deck's refusal or none.
func TestDestroyRetainedBlocked(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*stubSystem)
		want    DatasetBlock
	}{
		{
			name: "a hold",
			arrange: func(s *stubSystem) {
				s.holds = []Hold{{Snapshot: retainedOld + "@one", Tag: "keep"}}
			},
			want: DatasetBlock{
				Reason:   "refused: " + retainedOld + " has 1 hold(s).",
				Children: []string{}, Clones: []string{}, Holds: []string{retainedOld + "@one (keep)"},
			},
		},
		{
			name: "a clone",
			arrange: func(s *stubSystem) {
				s.clones[retainedOld+"@two"] = []string{"tank/jails/containers/from-old"}
			},
			want: DatasetBlock{
				Reason:   "refused: " + retainedOld + " has 1 dependent clone(s).",
				Children: []string{}, Clones: []string{"tank/jails/containers/from-old"}, Holds: []string{},
			},
		},
		{
			name: "a dataset of its own",
			arrange: func(s *stubSystem) {
				s.datasets = append(s.datasets, Dataset{Name: retainedOld + "/inner"})
			},
			want: DatasetBlock{
				Reason:   "refused: " + retainedOld + " has 1 dataset(s) of its own.",
				Children: []string{retainedOld + "/inner"}, Clones: []string{}, Holds: []string{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withDestroyable()
			tt.arrange(system)
			logger := &fakeLogger{}

			got, err := NewService(system, logger, "tank").DestroyRetained(context.Background(), "old", "old")
			if err != nil {
				t.Fatalf("DestroyRetained() error = %v", err)
			}
			if got.Destroyed || got.Blocked == nil {
				t.Fatalf("got %+v, want it blocked and not destroyed", got)
			}
			block := *got.Blocked
			reason, _, _ := strings.Cut(block.Reason, " None")
			block.Reason = reason
			if !reflect.DeepEqual(block, tt.want) {
				t.Errorf("blocked %+v\nwant    %+v", block, tt.want)
			}
			if len(got.Snapshots) != 2 {
				t.Errorf("snapshots = %q, want the two that would go named", got.Snapshots)
			}
			if len(system.ran) != 0 || len(logger.entries) != 0 {
				t.Errorf("ran %q and audited %d, want nothing", system.ran, len(logger.entries))
			}
		})
	}
}

// A host process with a file open on it makes the unmount refuse "busy", and
// nothing is destroyed: zfs destroy -r would have taken the snapshots before
// refusing at the unmount (measured, #120), so it is never reached.
func TestDestroyRetainedBusyDestroysNothing(t *testing.T) {
	system := withDestroyable()
	system.mountErr = commandError("exit status 1", "cannot unmount '/usr/local/jails/retained/old': pool or dataset is busy", 1)
	logger := &fakeLogger{}

	got, err := NewService(system, logger, "tank").DestroyRetained(context.Background(), "old", "old")
	if err == nil || !strings.Contains(err.Error(), "pool or dataset is busy") {
		t.Fatalf("error = %v, want zfs's sentence", err)
	}
	if want := []string{"zfs unmount " + retainedOld}; got.Destroyed || !reflect.DeepEqual(system.ran, want) {
		t.Errorf("destroyed = %v, ran %q; want only the unmount that refused", got.Destroyed, system.ran)
	}
	if len(logger.entries) != 1 || logger.entries[0].Success {
		t.Errorf("audit = %+v, want the one failed unmount", logger.entries)
	}
}

// A destroy that fails after the unmount mounts it again, so the dataset is
// left as it was found, and the destroy's own sentence is the answer.
func TestDestroyRetainedRemountsWhenTheDestroyFails(t *testing.T) {
	system := withDestroyable()
	system.writeErr = commandError("exit status 1", "cannot destroy: I/O error", 1)
	logger := &fakeLogger{}

	_, err := NewService(system, logger, "tank").DestroyRetained(context.Background(), "old", "old")
	if err == nil || !strings.Contains(err.Error(), "I/O error") {
		t.Fatalf("error = %v, want the destroy's sentence", err)
	}
	want := []string{"zfs unmount " + retainedOld, "zfs destroy -r " + retainedOld, "zfs mount " + retainedOld}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran %q, want %q", system.ran, want)
	}
	if system.notMounted[retainedOld] {
		t.Error("left unmounted after a destroy that did not happen")
	}
	if len(logger.entries) != 3 {
		t.Errorf("audit = %d entries, want one per command", len(logger.entries))
	}
}

// A jail's child resolves under the jail's own dataset and nowhere else.
func TestDestroyChildOfAJail(t *testing.T) {
	system := withDestroyable()
	system.snapshots = append(system.snapshots,
		Snapshot{Name: ottermqData + "@daily", Dataset: ottermqData, Short: "daily", CreateTXG: 30})

	got, err := NewJailSource(NewService(system, &fakeLogger{}, "tank")).
		DestroyChild(context.Background(), "/usr/local/jails/containers/ottermq", "data")
	if err != nil {
		t.Fatalf("DestroyChild() error = %v", err)
	}
	if want := []string{"zfs unmount " + ottermqData, "zfs destroy -r " + ottermqData}; !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran %q, want %q", system.ran, want)
	}
	if !got.Destroyed || !reflect.DeepEqual(got.Snapshots, []string{ottermqData + "@daily"}) {
		t.Errorf("got %+v, want data destroyed with @daily", got)
	}

	_, err = NewJailSource(NewService(withDestroyable(), &fakeLogger{}, "tank")).
		DestroyChild(context.Background(), "/usr/local/jails/containers/ottermq", "logs")
	if code := appErrCode(t, err); code != http.StatusNotFound {
		t.Errorf("a child that is not there: code = %d, want 404 (%v)", code, err)
	}
}

// Through the handler: a refusal is 409 with the result as its body, which is
// what lets a client show what is in the way instead of an error string.
func TestDestroyRetainedHandler(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*stubSystem)
		query   string
		want    int
	}{
		{name: "destroyed", query: "?confirm=old", want: http.StatusOK},
		{name: "held", arrange: func(s *stubSystem) { s.holds = []Hold{{Snapshot: retainedOld + "@one", Tag: "keep"}} },
			query: "?confirm=old", want: http.StatusConflict},
		{name: "no confirm", want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withDestroyable()
			if tt.arrange != nil {
				tt.arrange(system)
			}
			router := chi.NewRouter()
			RegisterStorageRoutes(router, NewStorageHandler(NewService(system, &fakeLogger{}, "tank")))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/retained/old"+tt.query, nil))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body)
			}
			if tt.want == http.StatusConflict {
				var got DatasetDestroy
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Blocked == nil || len(got.Blocked.Holds) != 1 {
					t.Errorf("body = %s, want the result naming the hold", rec.Body)
				}
			}
		})
	}
}
