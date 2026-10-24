package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// send is do() with a body, for the verbs that carry one.
func send(t *testing.T, router http.Handler, method, path string, cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return sendWithHeaders(t, router, method, path, cookies, body, nil)
}

func sendWithHeaders(t *testing.T, router http.Handler, method, path string, cookies []*http.Cookie, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

type apiChange struct {
	Dataset  string            `json:"dataset"`
	Created  bool              `json:"created"`
	Applied  map[string]string `json:"applied"`
	Conflict *struct {
		Property string `json:"property"`
		Want     string `json:"want"`
		Have     string `json:"have"`
	} `json:"conflict"`
}

// The create half of #3, through the router: a child dataset with a quota,
// then the same call again, which must not fail (JD-016).
func TestEnsureDatasetThroughTheAPI(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	const body = `{"properties":{"refquota":"10G","recordsize":"16K","compression":"lz4"}}`

	rec := send(t, router, http.MethodPut, "/api/jails/nginx/datasets/data", cookies, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first PUT returned %d, want 201: %s", rec.Code, rec.Body)
	}
	var change apiChange
	if err := json.Unmarshal(rec.Body.Bytes(), &change); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	if change.Dataset != "zroot/jails/nginx/data" || !change.Created {
		t.Errorf("change = %+v, want zroot/jails/nginx/data created", change)
	}

	// Repeated: 200, not created, no conflict. This is the retry an automation
	// client makes after a timeout.
	rec = send(t, router, http.MethodPut, "/api/jails/nginx/datasets/data", cookies, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("second PUT returned %d, want 200: %s", rec.Code, rec.Body)
	}
	change = apiChange{}
	json.Unmarshal(rec.Body.Bytes(), &change)
	if change.Created || change.Conflict != nil {
		t.Errorf("second PUT = %+v, want neither created nor conflicted", change)
	}

	// And the new dataset shows up as the jail's, matched by mountpoint.
	jails := listJails(t, router, cookies)
	if got := len(jails["nginx"].Datasets); got != 3 {
		t.Errorf("nginx has %d datasets, want 3 (its own, logs, and the new data)", got)
	}
}

func TestEnsureDatasetStatuses(t *testing.T) {
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
			name: "a property outside the allowlist",
			path: "/api/jails/nginx/datasets/data",
			body: `{"properties":{"sharenfs":"on"}}`,
			want: http.StatusBadRequest,
		},
		{
			// A dot is a legal ZFS name component, so this creates. A slash is
			// not testable from here at all: chi matches one path segment, so
			// datasets/a/b never reaches this handler -- checkChildName's
			// refusal of it is reachable only through the port directly, which
			// is where TestCheckChildName covers it.
			name: "a dot is a legal name component",
			path: "/api/jails/nginx/datasets/a.b",
			body: `{"properties":{}}`,
			want: http.StatusCreated,
		},
		{
			name: "a name ZFS will not accept",
			path: "/api/jails/nginx/datasets/-data",
			body: `{"properties":{}}`,
			want: http.StatusBadRequest,
		},
		{
			name: "an unknown jail",
			path: "/api/jails/does-not-exist/datasets/data",
			body: `{"properties":{}}`,
			want: http.StatusNotFound,
		},
		{
			name: "a jail with no path is on no dataset",
			path: "/api/jails/postgres/datasets/data",
			body: `{"properties":{}}`,
			want: http.StatusNotFound,
		},
		{
			name: "a misspelled field is refused, not ignored",
			path: "/api/jails/nginx/datasets/data",
			body: `{"propertys":{"refquota":"1G"}}`,
			want: http.StatusBadRequest,
		},
		{
			// mountpoint is settable only at creation (JD-023), so a child
			// that is already somewhere else is reported with both values
			// rather than moved.
			name: "a child that exists somewhere else",
			path: "/api/jails/nginx/datasets/logs",
			body: `{"properties":{"mountpoint":"/fake/jail/nginx/somewhere-else"}}`,
			want: http.StatusConflict,
		},
		{
			// A child of the jail mounted outside it is one the jail cannot
			// see and that destroying the jail would destroy anyway (JD-007).
			name: "a child mounted outside the jail",
			path: "/api/jails/nginx/datasets/logs",
			body: `{"properties":{"mountpoint":"/srv/elsewhere"}}`,
			want: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := send(t, router, http.MethodPut, tt.path, cookies, tt.body); rec.Code != tt.want {
				t.Errorf("PUT %s returned %d, want %d: %s", tt.path, rec.Code, tt.want, rec.Body)
			}
		})
	}
}

// Tagging, which is what #2's listing reads back.
func TestSetUserPropertiesThroughTheAPI(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	// The jail's own dataset: set one, remove one it already carries.
	rec := send(t, router, http.MethodPatch, "/api/jails/nginx/properties", cookies,
		`{"otter:env":"prod","otter:tier":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH properties returned %d: %s", rec.Code, rec.Body)
	}
	var found map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &found); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	want := map[string]string{"otter:instance": "web-01", "otter:env": "prod"}
	if len(found) != len(want) {
		t.Errorf("properties = %v, want %v", found, want)
	}
	for name, value := range want {
		if found[name] != value {
			t.Errorf("%s = %q, want %q", name, found[name], value)
		}
	}

	// And the listing agrees, which is the whole point of writing them here.
	if got := listJails(t, router, cookies)["nginx"].Properties; got["otter:env"] != "prod" {
		t.Errorf("listing properties = %v, want otter:env prod", got)
	}

	t.Run("a child dataset has its own", func(t *testing.T) {
		rec := send(t, router, http.MethodPatch, "/api/jails/nginx/datasets/logs/properties", cookies,
			`{"otter:retention":"30d"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH child properties returned %d: %s", rec.Code, rec.Body)
		}
		// The child's tag must not surface as the jail's.
		if got := listJails(t, router, cookies)["nginx"].Properties; got["otter:retention"] != "" {
			t.Errorf("the jail reports %v, want the child's tag to stay the child's", got)
		}
	})

	t.Run("a native property is refused", func(t *testing.T) {
		rec := send(t, router, http.MethodPatch, "/api/jails/nginx/properties", cookies, `{"compression":"gzip"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH compression returned %d, want 400: %s", rec.Code, rec.Body)
		}
	})

	t.Run("needs a session", func(t *testing.T) {
		rec := send(t, router, http.MethodPatch, "/api/jails/nginx/properties", nil, `{"otter:x":"y"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated PATCH returned %d, want 401", rec.Code)
		}
	})
}

// Every write is audited, and the request id reaches those entries too (#14).
func TestStorageWritesAreAudited(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	req := httptest.NewRequest(http.MethodPut, "/api/jails/nginx/datasets/data",
		strings.NewReader(`{"properties":{"refquota":"5G"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "otter.job.77")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("PUT returned %d: %s", rec.Code, rec.Body)
	}

	entries := auditEntries(t, router, cookies, "?request_id=otter.job.77")
	if len(entries) != 1 {
		t.Fatalf("found %d audit entries for the request id, want 1: %+v", len(entries), entries)
	}
	if entries[0].Operation != "create-dataset" || entries[0].Target != "zroot/jails/nginx/data" {
		t.Errorf("entry = %+v, want create-dataset on zroot/jails/nginx/data", entries[0])
	}
}

// listJails returns the listing keyed by name, for the assertions above.
func listJails(t *testing.T, router http.Handler, cookies []*http.Cookie) map[string]apiJail {
	t.Helper()
	rec := do(t, router, http.MethodGet, "/api/jails", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/jails returned %d: %s", rec.Code, rec.Body)
	}
	var jails []apiJail
	if err := json.Unmarshal(rec.Body.Bytes(), &jails); err != nil {
		t.Fatalf("decode jails: %v", err)
	}
	found := map[string]apiJail{}
	for _, jail := range jails {
		found[jail.Name] = jail
	}
	return found
}
