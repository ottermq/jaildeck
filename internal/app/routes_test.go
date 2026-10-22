package app

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
)

func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html":             &fstest.MapFile{Data: []byte("<!doctype html><title>JDeck</title>")},
		"assets/index-abc123.js": &fstest.MapFile{Data: []byte("console.log(1)")},
		"favicon.svg":            &fstest.MapFile{Data: []byte("<svg/>")},
	}
}

func TestRoutesRequireAuthentication(t *testing.T) {
	publicRoutes := map[string]struct{}{
		"POST /api/auth/login":  {},
		"POST /api/auth/logout": {},
	}
	var router http.Handler
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router = a.Routes(testAssets())
	param := regexp.MustCompile(`\{[^}]+\}`)
	seen := map[string]struct{}{}

	err = chi.Walk(router.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, param.ReplaceAllString(route, "placeholder"), nil))

		if _, public := publicRoutes[key]; public {
			seen[key] = struct{}{}
			if rec.Code == http.StatusUnauthorized {
				t.Errorf("%s is listed public but returned 401", key)
			}
			return nil
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s returned %d without a session, want 401 — protect it or list it in publicRoutes", key, rec.Code)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	for key := range publicRoutes {
		if _, ok := seen[key]; !ok {
			t.Errorf("publicRoutes lists %q, which is no longer registered", key)
		}
	}
}

// These paths served the JSON API before #57 moved it under /api. They must keep
// falling through to the SPA: a root-level route registered here later — a
// backwards-compatibility alias, a stray /healthz — would shadow the client-side
// router for that path, silently.
func TestFormerAPIPathsFallThroughtTheSPA(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	for _, path := range []string{"/jails", "/audit", "/auth/login", "/auth/me"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s returned %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s served %q, want text/html — the API must not be reachable here", path, ct)
		}
	}
}

func TestAuthenticatedAPIRootRedirectsToAPIJails(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	login := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"test-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(login, loginReq)
	if login.Code != http.StatusOK {
		t.Fatalf("login returned %d, want 200", login.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/", nil)
	for _, cookie := range login.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("/api/ returned %d, want 303", rec.Code)
	}

	if got := rec.Header().Get("Location"); got != "/api/jails" {
		t.Errorf("/api/ redirected to %q, want %q", got, "/api/jails")
	}
}

func TestUnknownAPIRouteReturnsJSON(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	rec := httptest.NewRecorder()
	a.Routes(testAssets()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestSPAIsPublic(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	assets := testAssets()
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	router := a.Routes(assets)
	for _, path := range []string{"/nope", "/"} {
		t.Run(path, func(t *testing.T) {
			// No session cookie: the login page must remain accessible.
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want text/html", ct)
			}
			if got := rec.Body.String(); got != string(index) {
				t.Errorf("body = %q, want %q", got, index)
			}
		})
	}
}

func TestExistingAssetServesItsContent(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	rec := httptest.NewRecorder()
	a.Routes(testAssets()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	if got := rec.Body.String(); got != "<svg/>" {
		t.Errorf("body = %q, want <svg/>", got)
	}
}

// The cache policy has three cases and getting them backwards is silent. A
// hashed filename can never change content, so it is the one thing safe to
// cache forever. A file from public/ keeps its name across builds, so caching
// it hard ships a change nobody receives. And a cached index.html outlives the
// asset hashes it references, leaving a blank page a reload will not fix.
func TestStaticCachePolicy(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	for _, tt := range []struct {
		path    string
		want    string
		mustNot string
	}{
		{path: "/assets/index-abc123.js", want: "immutable"},
		{path: "/favicon.svg", want: "no-cache", mustNot: "immutable"},
		{path: "/", want: "no-cache", mustNot: "immutable"},
		{path: "/jails", want: "no-cache", mustNot: "immutable"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			cc := rec.Header().Get("Cache-Control")
			if !strings.Contains(cc, tt.want) {
				t.Errorf("Cache-Control = %q, want it to contain %q", cc, tt.want)
			}
			if tt.mustNot != "" && strings.Contains(cc, tt.mustNot) {
				t.Errorf("Cache-Control = %q, must not contain %q", cc, tt.mustNot)
			}
		})
	}
}

func TestMissingIndexServesNotBuiltPage(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	assets := testAssets().(fstest.MapFS)
	delete(assets, "index.html")
	router := a.Routes(assets)
	for _, path := range []string{"/", "/nope"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want text/html", ct)
			}
			if got := rec.Body.String(); got != notBuilt {
				t.Errorf("body = %q, want %q", got, notBuilt)
			}
		})
	}
}

// The fake host is what make run-fake serves, and its report must carry all
// three check states for the web interface to have each one to render.
func TestHostReadinessOverFakeAdapter(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	login := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"test-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(login, loginReq)
	if login.Code != http.StatusOK {
		t.Fatalf("login returned %d, want 200", login.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/host/readiness", nil)
	for _, cookie := range login.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/host/readiness returned %d: %s", rec.Code, rec.Body)
	}
	var report struct {
		Capabilities []struct{ Status string } `json:"capabilities"`
		Checks       []struct{ Status string } `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range report.Checks {
		seen[c.Status] = true
	}
	for _, status := range []string{"pass", "fail", "unknown"} {
		if !seen[status] {
			t.Errorf("fake report has no %q check; checks = %+v", status, report.Checks)
		}
	}
	if len(report.Capabilities) != 6 {
		t.Errorf("capabilities = %d, want 6", len(report.Capabilities))
	}
}
