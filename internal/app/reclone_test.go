package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

type apiReclone struct {
	Root     string   `json:"root"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	OldRoot  string   `json:"old_root"`
	Children []string `json:"children"`
	Running  bool     `json:"running"`
	FailedAt string   `json:"failed_at"`
	Parked   []struct {
		Was string `json:"was"`
		Now string `json:"now"`
	} `json:"parked"`
}

const (
	fakeOldTemplate = "zroot/jails/templates/15.1-RELEASE@base"
	fakeNewTemplate = "zroot/jails/templates/15.1-RELEASE@p5"
)

// The upgrade path, end to end through the router: a running jail cloned from
// @base comes back running, cloned from @p5, with its child dataset intact and
// its old root parked (#6).
func TestRecloneAJail(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := send(t, router, http.MethodPost, "/api/jails/nginx/reclone?confirm=nginx", cookies,
		`{"template":"`+fakeNewTemplate+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reclone returned %d: %s", rec.Code, rec.Body)
	}
	var result apiReclone
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}

	if result.From != fakeOldTemplate || result.To != fakeNewTemplate {
		t.Errorf("from %q to %q, want %q to %q", result.From, result.To, fakeOldTemplate, fakeNewTemplate)
	}
	if result.OldRoot != "zroot/jails/retained/nginx-root-base" {
		t.Errorf("OldRoot = %q, want the previous root parked under its own snapshot name", result.OldRoot)
	}
	if len(result.Children) != 1 || result.Children[0] != "zroot/jails/nginx/logs" {
		t.Errorf("Children = %q, want the logs dataset back under the jail", result.Children)
	}
	if len(result.Parked) != 0 || result.FailedAt != "" {
		t.Errorf("result = %+v, want nothing outstanding", result)
	}

	// The jail is running again, because it was running before.
	if !result.Running {
		t.Error("Running = false, want the jail put back as it was found")
	}
	jails := listJails(t, router, cookies)
	if jails["nginx"].Path == "" || len(jails["nginx"].Datasets) != 2 {
		t.Errorf("nginx = %+v, want its root and its child", jails["nginx"].Datasets)
	}

	// Nothing was destroyed: the old root is still there, listed as retained.
	retained := retainedList(t, router, cookies)
	if len(retained) != 1 || retained[0].Short != "nginx-root-base" {
		t.Errorf("retained = %+v, want the old root kept", retained)
	}
}

func TestRecloneRefusals(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	tests := []struct {
		name string
		path string
		body string
		want int
	}{
		{
			name: "no confirm",
			path: "/api/jails/nginx/reclone",
			body: `{"template":"` + fakeNewTemplate + `"}`,
			want: http.StatusBadRequest,
		},
		{
			name: "confirm names another jail",
			path: "/api/jails/nginx/reclone?confirm=redis",
			body: `{"template":"` + fakeNewTemplate + `"}`,
			want: http.StatusBadRequest,
		},
		{
			name: "no such template snapshot",
			path: "/api/jails/nginx/reclone?confirm=nginx",
			body: `{"template":"zroot/jails/templates/15.1-RELEASE@p9"}`,
			want: http.StatusNotFound,
		},
		{
			name: "not a snapshot name",
			path: "/api/jails/nginx/reclone?confirm=nginx",
			body: `{"template":"zroot/jails/templates/15.1-RELEASE"}`,
			want: http.StatusBadRequest,
		},
		{
			name: "an unknown jail",
			path: "/api/jails/nosuch/reclone?confirm=nosuch",
			body: `{"template":"` + fakeNewTemplate + `"}`,
			want: http.StatusNotFound,
		},
		{
			name: "a jail that is not on ZFS",
			path: "/api/jails/postgres/reclone?confirm=postgres",
			body: `{"template":"` + fakeNewTemplate + `"}`,
			want: http.StatusNotFound,
		},
		{
			name: "a misspelled field",
			path: "/api/jails/nginx/reclone?confirm=nginx",
			body: `{"templates":"` + fakeNewTemplate + `"}`,
			want: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := send(t, router, http.MethodPost, tt.path, cookies, tt.body)
			if rec.Code != tt.want {
				t.Errorf("POST %s returned %d, want %d: %s", tt.path, rec.Code, tt.want, rec.Body)
			}
		})
	}

	// Every one of those refused before touching anything, so the jail is
	// still running and still has its datasets. A refusal that stopped the
	// jail on the way to saying no would be worse than no check at all.
	jails := listJails(t, router, cookies)
	if jails["nginx"].Status != "running" {
		t.Errorf("nginx is %q after seven refused re-clones, want running", jails["nginx"].Status)
	}
	if len(retainedList(t, router, cookies)) != 0 {
		t.Error("a refused re-clone parked something")
	}
}

// Every command of a re-clone is audited under the request's own id, including
// the stop and the start.
func TestRecloneIsAuditedEndToEnd(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := sendWithHeaders(t, router, http.MethodPost, "/api/jails/nginx/reclone?confirm=nginx", cookies,
		`{"template":"`+fakeNewTemplate+`"}`, map[string]string{"X-Request-Id": "otter.job.6"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reclone returned %d: %s", rec.Code, rec.Body)
	}

	operations := map[string]int{}
	for _, entry := range auditEntries(t, router, cookies, "?request_id=otter.job.6&limit=100") {
		operations[entry.Operation]++
	}
	for _, want := range []string{"stop", "start", "rename-dataset", "clone-dataset", "mount-dataset"} {
		if operations[want] == 0 {
			t.Errorf("no %s entry; the whole sequence must be accountable: %+v", want, operations)
		}
	}
	// And nothing destroyed anything.
	for operation := range operations {
		if operation == "destroy-snapshot" || operation == "destroy-dataset" {
			t.Errorf("a re-clone ran %s", operation)
		}
	}
}
