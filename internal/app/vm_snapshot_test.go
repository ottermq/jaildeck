package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

// A guest is snapshotted through the router with its disk, a running one said
// to be crash-consistent, and the retry answers 200 (JD-045).
func TestSnapshotAGuest(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	type result struct {
		Snapshot    string   `json:"snapshot"`
		Created     []string `json:"created"`
		Existed     bool     `json:"existed"`
		Consistency string   `json:"consistency"`
	}
	put := func(path string, want int) result {
		t.Helper()
		rec := do(t, router, http.MethodPut, path, cookies, nil)
		if rec.Code != want {
			t.Fatalf("PUT %s returned %d, want %d: %s", path, rec.Code, want, rec.Body)
		}
		var got result
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v\n%s", err, rec.Body)
		}
		return got
	}

	got := put("/api/vms/jd-cap-running/snapshots/before", http.StatusCreated)
	if got.Snapshot != "zroot/vm/jd-cap-running@before" || got.Consistency != "crash-consistent" {
		t.Errorf("running guest: %+v, want its dataset's snapshot, crash-consistent", got)
	}
	if want := []string{"zroot/vm/jd-cap-running@before", "zroot/vm/jd-cap-running/disk0@before"}; !slices.Equal(got.Created, want) {
		t.Errorf("created %q, want the guest and its disk", got.Created)
	}
	got = put("/api/vms/jd-cap-running/snapshots/before", http.StatusOK)
	if !got.Existed || got.Consistency != "" {
		t.Errorf("retry: %+v, want existed with no consistency", got)
	}
	got = put("/api/vms/jd-cap-stopped/snapshots/before", http.StatusCreated)
	if got.Consistency != "consistent" {
		t.Errorf("stopped guest: %+v, want consistent", got)
	}

	for _, tt := range []struct {
		path string
		want int
	}{
		{"/api/vms/nosuch/snapshots/before", http.StatusNotFound},
		{"/api/vms/jd-cap-stopped/snapshots/a@b", http.StatusBadRequest},
	} {
		if rec := do(t, router, http.MethodPut, tt.path, cookies, nil); rec.Code != tt.want {
			t.Errorf("PUT %s returned %d, want %d: %s", tt.path, rec.Code, tt.want, rec.Body)
		}
	}
}
