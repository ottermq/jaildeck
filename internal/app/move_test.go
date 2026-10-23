package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

type apiMove struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Mountpoint string `json:"mountpoint"`
	Remounted  bool   `json:"remounted"`
}

type apiRetained struct {
	Name       string `json:"name"`
	Short      string `json:"short"`
	Mountpoint string `json:"mountpoint"`
}

func retainedList(t *testing.T, router http.Handler, cookies []*http.Cookie) []apiRetained {
	t.Helper()
	rec := do(t, router, http.MethodGet, "/api/retained", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/retained returned %d: %s", rec.Code, rec.Body)
	}
	var retained []apiRetained
	if err := json.Unmarshal(rec.Body.Bytes(), &retained); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	return retained
}

// The round trip #5 asks for, through the router: out of one jail, listed
// while it is homeless, and back into another.
func TestRetainAndRestore(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if got := retainedList(t, router, cookies); len(got) != 0 {
		t.Fatalf("retained = %+v, want nothing before anything is retained", got)
	}

	rec := send(t, router, http.MethodPost, "/api/jails/nginx/datasets/logs/retain", cookies,
		`{"name":"nginx-logs"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("retain returned %d: %s", rec.Code, rec.Body)
	}
	var moved apiMove
	if err := json.Unmarshal(rec.Body.Bytes(), &moved); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	if moved.To != "zroot/jails/retained/nginx-logs" || !moved.Remounted {
		t.Errorf("moved = %+v, want it carried to retained and remounted", moved)
	}

	// It is no longer the jail's.
	if datasets := listJails(t, router, cookies)["nginx"].Datasets; len(datasets) != 1 {
		t.Errorf("nginx has %d datasets, want only its own: %+v", len(datasets), datasets)
	}
	// And it is findable, which is the only way a client knows what to restore
	// once the jail it belonged to is gone.
	retained := retainedList(t, router, cookies)
	if len(retained) != 1 || retained[0].Short != "nginx-logs" {
		t.Fatalf("retained = %+v, want the one dataset", retained)
	}

	// Back in, under a different jail and a different name than it left with.
	rec = send(t, router, http.MethodPost, "/api/jails/redis/datasets/adopted/restore", cookies,
		`{"from":"nginx-logs"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore returned %d: %s", rec.Code, rec.Body)
	}
	if datasets := listJails(t, router, cookies)["redis"].Datasets; len(datasets) != 2 {
		t.Errorf("redis has %d datasets, want its own and the adopted one: %+v", len(datasets), datasets)
	}
	if got := retainedList(t, router, cookies); len(got) != 0 {
		t.Errorf("retained = %+v, want nothing after the restore", got)
	}
}

func TestRetainRefusals(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	// A real second dataset to collide with: redis has no children of its own,
	// and a retain of something that does not exist is a 404 that would never
	// reach the conflict this is about.
	if rec := send(t, router, http.MethodPut, "/api/jails/redis/datasets/x", cookies,
		`{"properties":{}}`); rec.Code != http.StatusCreated {
		t.Fatalf("creating redis/x returned %d: %s", rec.Code, rec.Body)
	}
	if rec := send(t, router, http.MethodPost, "/api/jails/nginx/datasets/logs/retain", cookies,
		`{"name":"taken"}`); rec.Code != http.StatusOK {
		t.Fatalf("first retain returned %d: %s", rec.Code, rec.Body)
	}

	tests := []struct {
		name string
		path string
		body string
		want int
	}{
		{name: "the target name is taken", path: "/api/jails/redis/datasets/x/retain", body: `{"name":"taken"}`, want: http.StatusConflict},
		{name: "no such child dataset", path: "/api/jails/redis/datasets/nope/retain", body: `{"name":"fine"}`, want: http.StatusNotFound},
		{name: "not a dataset name", path: "/api/jails/redis/datasets/x/retain", body: `{"name":"../escape"}`, want: http.StatusBadRequest},
		{name: "an unknown jail", path: "/api/jails/nosuch/datasets/x/retain", body: `{"name":"fine"}`, want: http.StatusNotFound},
		{name: "a jail with no path", path: "/api/jails/postgres/datasets/x/retain", body: `{"name":"fine"}`, want: http.StatusNotFound},
		{name: "a misspelled field", path: "/api/jails/redis/datasets/x/retain", body: `{"named":"fine"}`, want: http.StatusBadRequest},
		{name: "restoring something that is not retained", path: "/api/jails/redis/datasets/x/restore", body: `{"from":"absent"}`, want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := send(t, router, http.MethodPost, tt.path, cookies, tt.body); rec.Code != tt.want {
				t.Errorf("POST %s returned %d, want %d: %s", tt.path, rec.Code, tt.want, rec.Body)
			}
		})
	}
}

// Every command of a move is audited, under the request's own id.
func TestMovesAreAudited(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	req := sendWithHeaders(t, router, http.MethodPost, "/api/jails/nginx/datasets/logs/retain", cookies,
		`{"name":"nginx-logs"}`, map[string]string{"X-Request-Id": "otter.job.5"})
	if req.Code != http.StatusOK {
		t.Fatalf("retain returned %d: %s", req.Code, req.Body)
	}

	entries := auditEntries(t, router, cookies, "?request_id=otter.job.5")
	operations := map[string]int{}
	for _, entry := range entries {
		operations[entry.Operation]++
	}
	// create-dataset for the retained parent, rename-dataset, and two
	// mount-dataset lines for the unmount and the mount.
	want := map[string]int{"create-dataset": 1, "rename-dataset": 1, "mount-dataset": 2}
	for operation, n := range want {
		if operations[operation] != n {
			t.Errorf("%s appears %d times, want %d: %+v", operation, operations[operation], n, entries)
		}
	}
}
