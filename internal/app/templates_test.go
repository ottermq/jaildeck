package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

type apiTemplate struct {
	Name       string `json:"name"`
	Dataset    string `json:"dataset"`
	Mountpoint string `json:"mountpoint"`
	Ready      bool   `json:"ready"`
	Parent     string `json:"parent"`
	Snapshot   string `json:"snapshot"`
}

func templateList(t *testing.T, router http.Handler, cookies []*http.Cookie) map[string]apiTemplate {
	t.Helper()
	rec := do(t, router, http.MethodGet, "/api/templates", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/templates returned %d: %s", rec.Code, rec.Body)
	}
	var list []apiTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	found := map[string]apiTemplate{}
	for _, template := range list {
		found[template.Name] = template
	}
	return found
}

// The acceptance criterion of #7: a derived template can be prepared and
// listed with its parent.
func TestDeriveATemplate(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	// The release template is there and ready, with no parent.
	before := templateList(t, router, cookies)
	if base, ok := before["15.1-RELEASE"]; !ok || !base.Ready || base.Parent != "" {
		t.Fatalf("15.1-RELEASE = %+v, want a ready template with no parent", base)
	}

	rec := send(t, router, http.MethodPut, "/api/templates/postgres-16", cookies,
		`{"from":"15.1-RELEASE","packages":["postgresql16-server"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("derive returned %d, want 201: %s", rec.Code, rec.Body)
	}
	var derived apiTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &derived); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	if !derived.Ready || derived.Parent != "15.1-RELEASE" {
		t.Errorf("derived = %+v, want it ready with 15.1-RELEASE as its parent", derived)
	}

	// Listed with its parent, which is read back from the clone's origin
	// rather than recorded anywhere.
	after := templateList(t, router, cookies)
	if got, ok := after["postgres-16"]; !ok || got.Parent != "15.1-RELEASE" || !got.Ready {
		t.Errorf("postgres-16 = %+v, want it listed, ready, with its parent", got)
	}

	// And repeating the call is the retry after a timeout, not a failure.
	if rec := send(t, router, http.MethodPut, "/api/templates/postgres-16", cookies,
		`{"from":"15.1-RELEASE","packages":["postgresql16-server"]}`); rec.Code != http.StatusOK {
		t.Errorf("second derive returned %d, want 200: %s", rec.Code, rec.Body)
	}
}

// A failed install leaves a template that exists and is not ready, which the
// listing says plainly. No cleanup is needed for that state to be truthful.
func TestAFailedDeriveLeavesAnUnreadyTemplate(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := send(t, router, http.MethodPut, "/api/templates/broken", cookies,
		`{"from":"15.1-RELEASE","packages":["nosuchpackage"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("derive with a bad package returned %d, want 400: %s", rec.Code, rec.Body)
	}

	found := templateList(t, router, cookies)
	broken, ok := found["broken"]
	if !ok {
		t.Fatal("the half-built template is not in the listing; it should be, unready")
	}
	if broken.Ready || broken.Snapshot != "" {
		t.Errorf("broken = %+v, want it present and not ready", broken)
	}

	// And a second attempt refuses rather than installing on top of it.
	if rec := send(t, router, http.MethodPut, "/api/templates/broken", cookies,
		`{"from":"15.1-RELEASE","packages":["something-else"]}`); rec.Code != http.StatusConflict {
		t.Errorf("retrying a failed derive returned %d, want 409: %s", rec.Code, rec.Body)
	}
}

func TestTemplateRefusals(t *testing.T) {
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
		{name: "no such parent", path: "/api/templates/x", body: `{"from":"14.2-RELEASE"}`, want: http.StatusNotFound},
		{name: "a package pkg would read as a flag", path: "/api/templates/x",
			body: `{"from":"15.1-RELEASE","packages":["-f"]}`, want: http.StatusBadRequest},
		{name: "a misspelled field", path: "/api/templates/x",
			body: `{"parent":"15.1-RELEASE"}`, want: http.StatusBadRequest},
		{name: "a name ZFS will not take", path: "/api/templates/-x",
			body: `{"from":"15.1-RELEASE"}`, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := send(t, router, http.MethodPut, tt.path, cookies, tt.body); rec.Code != tt.want {
				t.Errorf("PUT %s returned %d, want %d: %s", tt.path, rec.Code, tt.want, rec.Body)
			}
		})
	}

	t.Run("needs a session", func(t *testing.T) {
		if rec := do(t, router, http.MethodGet, "/api/templates", nil, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated GET /api/templates returned %d, want 401", rec.Code)
		}
	})
}

func TestTemplateInstallIsAudited(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := sendWithHeaders(t, router, http.MethodPut, "/api/templates/postgres-16", cookies,
		`{"from":"15.1-RELEASE","packages":["postgresql16-server"]}`,
		map[string]string{"X-Request-Id": "otter.job.7"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("derive returned %d: %s", rec.Code, rec.Body)
	}

	operations := map[string]string{}
	for _, entry := range auditEntries(t, router, cookies, "?request_id=otter.job.7") {
		operations[entry.Operation] = entry.Target
	}
	for _, want := range []string{"clone-dataset", "install-packages", "create-snapshot"} {
		if _, ok := operations[want]; !ok {
			t.Errorf("no %s entry; the whole sequence must be accountable: %+v", want, operations)
		}
	}
}

// Cloning a jail as a template, Handbook 17.4.3 (#7, JD-027). The other way to
// fill a template: whatever you configured in the jail, not only packages.
func TestCloneAJailAsATemplate(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	// A running jail is refused rather than stopped: a snapshot of one is an
	// image caught mid-stride, and taking a service down is the operator's.
	rec := send(t, router, http.MethodPost, "/api/jails/nginx/template", cookies, `{"name":"nginx-base"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cloning a running jail returned %d, want 409: %s", rec.Code, rec.Body)
	}

	if rec := do(t, router, http.MethodPost, "/api/jails/nginx/stop", cookies, nil); rec.Code != http.StatusOK {
		t.Fatalf("stop returned %d: %s", rec.Code, rec.Body)
	}
	rec = send(t, router, http.MethodPost, "/api/jails/nginx/template", cookies, `{"name":"nginx-base"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("clone as template returned %d, want 201: %s", rec.Code, rec.Body)
	}

	// It lists like any other template, read from ZFS: ready, with its parent.
	found := templateList(t, router, cookies)
	made, ok := found["nginx-base"]
	if !ok {
		t.Fatalf("nginx-base is not in the listing: %+v", found)
	}
	if !made.Ready {
		t.Error("the template is not ready; @base should have moved to it on promote")
	}
	if made.Parent != "15.1-RELEASE" {
		t.Errorf("parent = %q, want the release template the jail came from", made.Parent)
	}

	// And it can be derived from in turn, which is the whole point of it
	// being an ordinary template.
	if rec := send(t, router, http.MethodPut, "/api/templates/nginx-tuned", cookies,
		`{"from":"nginx-base","packages":[]}`); rec.Code != http.StatusCreated {
		t.Errorf("deriving from it returned %d: %s", rec.Code, rec.Body)
	}
}

func TestCloneAJailAsATemplateRefusals(t *testing.T) {
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
		{name: "a jail that is not on ZFS", path: "/api/jails/postgres/template",
			body: `{"name":"x"}`, want: http.StatusNotFound},
		{name: "an unknown jail", path: "/api/jails/nosuch/template",
			body: `{"name":"x"}`, want: http.StatusNotFound},
		{name: "a misspelled field", path: "/api/jails/postgres/template",
			body: `{"template":"x"}`, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := send(t, router, http.MethodPost, tt.path, cookies, tt.body); rec.Code != tt.want {
				t.Errorf("POST %s returned %d, want %d: %s", tt.path, rec.Code, tt.want, rec.Body)
			}
		})
	}
}
