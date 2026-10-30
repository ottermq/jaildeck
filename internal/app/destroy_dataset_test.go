package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

type apiDatasetDestroy struct {
	Dataset   string   `json:"dataset"`
	Snapshots []string `json:"snapshots"`
	Destroyed bool     `json:"destroyed"`
	Blocked   *struct {
		Reason string `json:"reason"`
	} `json:"blocked"`
}

// The end of a retain, through the router (#120): retained, destroyed with its
// snapshot, gone from the listing, and 404 when asked again.
func TestDestroyRetained(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if rec := send(t, router, http.MethodPut, "/api/jails/nginx/datasets/logs/snapshots/last", cookies, ""); rec.Code != http.StatusCreated {
		t.Fatalf("snapshot returned %d: %s", rec.Code, rec.Body)
	}
	if rec := send(t, router, http.MethodPost, "/api/jails/nginx/datasets/logs/retain", cookies,
		`{"name":"nginx-logs"}`); rec.Code != http.StatusOK {
		t.Fatalf("retain returned %d: %s", rec.Code, rec.Body)
	}

	if rec := do(t, router, http.MethodDelete, "/api/retained/nginx-logs", cookies, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("without confirm: %d, want 400: %s", rec.Code, rec.Body)
	}
	rec := do(t, router, http.MethodDelete, "/api/retained/nginx-logs?confirm=nginx-logs", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("destroy returned %d: %s", rec.Code, rec.Body)
	}
	var got apiDatasetDestroy
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	want := "zroot/jails/retained/nginx-logs"
	// The fake's logs carries @before-upgrade already; @last is the one taken here.
	if got.Dataset != want || !got.Destroyed || !slices.Contains(got.Snapshots, want+"@last") {
		t.Errorf("got %+v, want %s destroyed, @last among its snapshots", got, want)
	}
	if retained := retainedList(t, router, cookies); len(retained) != 0 {
		t.Errorf("retained = %+v, want nothing after the destroy", retained)
	}
	if rec := do(t, router, http.MethodDelete, "/api/retained/nginx-logs?confirm=nginx-logs", cookies, nil); rec.Code != http.StatusNotFound {
		t.Errorf("again: %d, want 404: %s", rec.Code, rec.Body)
	}
}

// A child of a running jail is refused, and goes once the jail is stopped,
// leaving the jail and its own dataset.
func TestDestroyChildDataset(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	const path = "/api/jails/nginx/datasets/logs?confirm=logs"
	rec := do(t, router, http.MethodDelete, path, cookies, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stop the jail first") {
		t.Fatalf("while running: %d, want 409 saying to stop it: %s", rec.Code, rec.Body)
	}
	if rec := send(t, router, http.MethodPost, "/api/jails/nginx/stop", cookies, ""); rec.Code != http.StatusOK {
		t.Fatalf("stop returned %d: %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodDelete, "/api/jails/nginx/datasets/logs?confirm=nginx", cookies, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("confirm of the jail's name: %d, want 400: %s", rec.Code, rec.Body)
	}
	if rec = do(t, router, http.MethodDelete, path, cookies, nil); rec.Code != http.StatusOK {
		t.Fatalf("stopped: %d, want 200: %s", rec.Code, rec.Body)
	}
	if datasets := listJails(t, router, cookies)["nginx"].Datasets; len(datasets) != 1 {
		t.Errorf("nginx has %d datasets, want only its own: %+v", len(datasets), datasets)
	}
	if rec := do(t, router, http.MethodDelete, path, cookies, nil); rec.Code != http.StatusNotFound {
		t.Errorf("again: %d, want 404: %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodDelete, "/api/jails/nginx/datasets/..?confirm=..", cookies, nil); rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Errorf("a path out of the jail: %d, want it refused: %s", rec.Code, rec.Body)
	}
}
