package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type apiTemplate struct {
	Name       string `json:"name"`
	Dataset    string `json:"dataset"`
	Mountpoint string `json:"mountpoint"`
	Ready      bool   `json:"ready"`
	Parent     string `json:"parent"`
	Snapshot   string `json:"snapshot"`
	State      string `json:"state"`
	Running    *struct {
		Step  string `json:"step"`
		Since string `json:"since"`
	} `json:"running"`
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

// Removing a template, and the refusal the roadmap asks for (#7, JD-027).
func TestRemoveATemplate(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if rec := send(t, router, http.MethodPut, "/api/templates/scratch", cookies,
		`{"from":"15.1-RELEASE","packages":[]}`); rec.Code != http.StatusCreated {
		t.Fatalf("derive returned %d: %s", rec.Code, rec.Body)
	}

	t.Run("nothing depends on it", func(t *testing.T) {
		rec := do(t, router, http.MethodDelete, "/api/templates/scratch?confirm=scratch", cookies, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("DELETE returned %d, want 200: %s", rec.Code, rec.Body)
		}
		if _, ok := templateList(t, router, cookies)["scratch"]; ok {
			t.Error("the template is still listed after being destroyed")
		}
	})

	t.Run("the release template has jails on it", func(t *testing.T) {
		rec := do(t, router, http.MethodDelete,
			"/api/templates/15.1-RELEASE?confirm=15.1-RELEASE", cookies, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("DELETE returned %d, want 409: %s", rec.Code, rec.Body)
		}
		var removal struct {
			Destroyed bool `json:"destroyed"`
			Blocked   *struct {
				Reason string   `json:"reason"`
				Clones []string `json:"clones"`
			} `json:"blocked"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &removal); err != nil {
			t.Fatalf("decode: %v\n%s", err, rec.Body)
		}
		if removal.Destroyed {
			t.Error("destroyed = true on a refusal")
		}
		if removal.Blocked == nil || len(removal.Blocked.Clones) == 0 {
			t.Fatalf("blocked = %+v, want it to name the jails cloned from it", removal.Blocked)
		}
		// Named, not merely counted: zfs's own refusal would say "dependent
		// clones" and leave the operator to find out which.
		found := false
		for _, clone := range removal.Blocked.Clones {
			if clone == "zroot/jails/nginx" || clone == "zroot/jails/redis" {
				found = true
			}
		}
		if !found {
			t.Errorf("clones = %q, want the jails cloned from it", removal.Blocked.Clones)
		}
		// And it is still there.
		if _, ok := templateList(t, router, cookies)["15.1-RELEASE"]; !ok {
			t.Error("the template was destroyed despite the refusal")
		}
	})

	t.Run("statuses", func(t *testing.T) {
		for path, want := range map[string]int{
			"/api/templates/15.1-RELEASE":                   http.StatusBadRequest,
			"/api/templates/15.1-RELEASE?confirm=something": http.StatusBadRequest,
			"/api/templates/absent?confirm=absent":          http.StatusNotFound,
		} {
			if rec := do(t, router, http.MethodDelete, path, cookies, nil); rec.Code != want {
				t.Errorf("DELETE %s returned %d, want %d: %s", path, rec.Code, want, rec.Body)
			}
		}
	})
}

// The half-built template a failed derive leaves behind can now be cleaned up
// through the API, which is what this endpoint was added for.
func TestRemoveAHalfBuiltTemplate(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if rec := send(t, router, http.MethodPut, "/api/templates/broken", cookies,
		`{"from":"15.1-RELEASE","packages":["nosuchpackage"]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("derive returned %d, want 400: %s", rec.Code, rec.Body)
	}
	if got := templateList(t, router, cookies)["broken"]; got.Ready {
		t.Fatal("the half-built template reports itself ready")
	}

	if rec := do(t, router, http.MethodDelete, "/api/templates/broken?confirm=broken", cookies, nil); rec.Code != http.StatusOK {
		t.Fatalf("DELETE returned %d: %s", rec.Code, rec.Body)
	}
	if _, ok := templateList(t, router, cookies)["broken"]; ok {
		t.Error("the half-built template is still listed")
	}
	// And the derive that failed can now be attempted again.
	if rec := send(t, router, http.MethodPut, "/api/templates/broken", cookies,
		`{"from":"15.1-RELEASE","packages":[]}`); rec.Code != http.StatusCreated {
		t.Errorf("retrying after cleanup returned %d, want 201: %s", rec.Code, rec.Body)
	}
}

// JD-008 through the router: the call answers 202 without waiting, and the
// template is in the listing from that moment, because it is the resource the
// client was told to watch. There is no job to ask about.
func TestPrepareAReleaseAnswersAtOnce(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := send(t, router, http.MethodPut, "/api/templates/14.3-RELEASE", cookies,
		`{"release":"14.3-RELEASE"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("PUT returned %d, want 202: %s", rec.Code, rec.Body)
	}

	var started apiTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	if started.Name != "14.3-RELEASE" {
		t.Errorf("name = %q, want the template that was asked for", started.Name)
	}
	if started.State != "fetching" && started.State != "fetched" {
		t.Errorf("state = %q, want it under way or done", started.State)
	}

	// Polling the template is the whole mechanism. It reaches fetched and
	// stops running, with no dataset: extracting it is a later stage.
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec := do(t, router, http.MethodGet, "/api/templates/14.3-RELEASE", cookies, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET returned %d: %s", rec.Code, rec.Body)
		}
		var polled apiTemplate
		if err := json.Unmarshal(rec.Body.Bytes(), &polled); err != nil {
			t.Fatalf("decode: %v\n%s", err, rec.Body)
		}
		if polled.State == "ready" {
			if polled.Running != nil {
				t.Errorf("running = %+v, want nothing once it is done", polled.Running)
			}
			if polled.Dataset == "" {
				t.Error("dataset is empty, want the one the extract created")
			}
			// Ready is the @base snapshot and nothing else: a jail can be
			// cloned from this now (JD-007).
			if !polled.Ready || polled.Snapshot == "" {
				t.Errorf("ready=%v snapshot=%q, want a sealed template", polled.Ready, polled.Snapshot)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("state stuck at %q, want ready", polled.State)
		}
		time.Sleep(time.Millisecond)
	}
}

// A release the mirror does not have leaves the template where it stopped, and
// the audit log is what says why (JD-008). Nothing is retried, nothing cleaned.
func TestPrepareAMissingReleaseLeavesNothingBehind(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if rec := send(t, router, http.MethodPut, "/api/templates/15.0-RELEASE", cookies,
		`{"release":"15.0-RELEASE"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("PUT returned %d, want 202: %s", rec.Code, rec.Body)
	}

	// The audit log is what says why, so it is what the test waits for: the
	// template itself goes quiet either way (JD-008).
	deadline := time.Now().Add(2 * time.Second)
	var audited string
	for {
		rec := do(t, router, http.MethodGet, "/api/audit?operation=fetch-base", cookies, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/audit returned %d: %s", rec.Code, rec.Body)
		}
		audited = rec.Body.String()
		if strings.Contains(audited, "15.0-RELEASE") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the audit log never mentioned the failed fetch: %s", audited)
		}
		time.Sleep(time.Millisecond)
	}
	if strings.Contains(audited, `"success":true`) {
		t.Errorf("the failed fetch is recorded as a success: %s", audited)
	}

	// And it left nothing: a release that was never fetched is not a template.
	rec := do(t, router, http.MethodGet, "/api/templates/15.0-RELEASE", cookies, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET returned %d, want 404 after a fetch that produced nothing: %s", rec.Code, rec.Body)
	}
}

// Naming both ways to build a template is a request that cannot be honoured,
// and saying so beats picking one.
func TestDeriveRefusesBothFromAndRelease(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if rec := send(t, router, http.MethodPut, "/api/templates/both", cookies,
		`{"from":"15.1-RELEASE","release":"14.3-RELEASE"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT returned %d, want 400: %s", rec.Code, rec.Body)
	}
}
