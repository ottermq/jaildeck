package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

type apiReclaimable struct {
	Items []struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Jail   string `json:"jail"`
		Size   int64  `json:"size"`
		Why    string `json:"why"`
		Remove *struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"remove"`
	} `json:"items"`
	Total            int64            `json:"total"`
	Kinds            map[string]int64 `json:"kinds"`
	SnapshotAge      int              `json:"snapshot_age_days"`
	SkippedSnapshots map[string]int   `json:"skipped_snapshots"`
	Problems         []string         `json:"problems"`
}

func reclaimable(t *testing.T, router http.Handler, cookies []*http.Cookie, query string) apiReclaimable {
	t.Helper()
	rec := do(t, router, http.MethodGet, "/api/host/reclaimable"+query, cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/host/reclaimable%s returned %d: %s", query, rec.Code, rec.Body)
	}
	var report apiReclaimable
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	return report
}

// #125: what nothing uses is listed with the request that removes it, and
// that request is the existing endpoint, which removes it.
func TestReclaimableListsWhatTheExistingEndpointsRemove(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatal(err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	before := reclaimable(t, router, cookies, "")
	if len(before.Problems) != 0 || before.SnapshotAge != 30 {
		t.Fatalf("report = %+v", before)
	}
	for _, item := range before.Items {
		if item.Kind == "template" && item.Name == "15.1-RELEASE" {
			t.Errorf("15.1-RELEASE has clones and is listed: %+v", item)
		}
	}

	if rec := send(t, router, http.MethodPut, "/api/templates/spare", cookies,
		`{"from":"15.1-RELEASE"}`); rec.Code != http.StatusCreated {
		t.Fatalf("derive returned %d: %s", rec.Code, rec.Body)
	}
	after := reclaimable(t, router, cookies, "")
	path := ""
	for _, item := range after.Items {
		if item.Kind == "template" && item.Name == "spare" && item.Remove != nil {
			path = item.Remove.Path
		}
	}
	if path != "/api/templates/spare?confirm=spare" {
		t.Fatalf("spare's removal = %q, items %+v", path, after.Items)
	}
	if rec := do(t, router, http.MethodDelete, path, cookies, nil); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE %s returned %d: %s", path, rec.Code, rec.Body)
	}
	for _, item := range reclaimable(t, router, cookies, "").Items {
		if item.Name == "spare" {
			t.Errorf("spare is still listed after its removal")
		}
	}

	if rec := do(t, router, http.MethodGet, "/api/host/reclaimable?older_than_days=x", cookies, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad age returned %d", rec.Code)
	}
	if rec := do(t, router, http.MethodGet, "/api/host/reclaimable", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session returned %d", rec.Code)
	}
}
