package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The README calls collections/insomnium.json "an Insomnium collection covering
// every endpoint". That promise has been broken twice — once a whole endpoint
// set was missing, once it still described an empty /audit result as null two
// releases after it became [] — because nothing failed when the file was wrong.
// These tests are what makes the claim true rather than hoped for.
//
// They check coverage, not prose. Whether a description names the gotcha a
// client would otherwise hit is a judgment no test makes.

// collectionPath is relative because go test runs in the package directory.
const collectionPath = "../../collections/insomnium.json"

// collection is the subset of the Insomnium v4 export these tests read.
type collection struct {
	Resources []struct {
		Type        string `json:"_type"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Method      string `json:"method"`
		URL         string `json:"url"`
		Data        struct {
			BaseURL string `json:"baseUrl"`
		} `json:"data"`
	} `json:"resources"`
}

// request is one collection entry, its {{baseUrl}} removed so what remains is
// comparable with a chi route pattern.
type request struct {
	name        string
	method      string
	path        string
	description string
}

func loadCollection(t *testing.T) (reqs []request, baseURLs []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(collectionPath))
	if err != nil {
		t.Fatalf("read the API collection: %v", err)
	}
	var c collection
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse the API collection: %v", err)
	}
	for _, r := range c.Resources {
		switch r.Type {
		case "environment":
			if r.Data.BaseURL != "" {
				baseURLs = append(baseURLs, r.Data.BaseURL)
			}
		case "request":
			path, ok := strings.CutPrefix(r.URL, "{{baseUrl}}")
			if !ok {
				t.Errorf("request %q has url %q, which does not start with {{baseUrl}}", r.Name, r.URL)
				continue
			}
			reqs = append(reqs, request{
				name: r.Name, method: r.Method, path: normalizePath(path),
				description: strings.TrimSpace(r.Description),
			})
		}
	}
	if len(reqs) == 0 {
		t.Fatal("the API collection holds no requests")
	}
	return reqs, baseURLs
}

// normalizePath makes a chi pattern and a collection URL comparable. chi
// reports the index route of a sub-router with a trailing slash (/api/audit/)
// where the collection has none.
func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// matches reports whether a collection path is an instance of a chi pattern. A
// {param} segment matches any single segment, so the collection may use an
// environment variable ({{jail}}) or a literal (does-not-exist) for it.
func matches(pattern, path string) bool {
	want := strings.Split(pattern, "/")
	got := strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i, seg := range want {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			continue
		}
		if seg != got[i] {
			return false
		}
	}
	return true
}

// apiRoutes is every route the router serves under /api, as a path pattern with
// the /api prefix removed, so it is comparable with a collection URL.
func apiRoutes(t *testing.T) map[string][]string {
	t.Helper()
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	routes := map[string][]string{}
	err = chi.Walk(a.Routes(testAssets()).(chi.Routes),
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			path, ok := strings.CutPrefix(route, "/api")
			if !ok {
				// The SPA fallback is not part of the JSON API.
				return nil
			}
			routes[method] = append(routes[method], normalizePath(path))
			return nil
		})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("the router serves no /api routes")
	}
	return routes
}

func TestCollectionCoversEveryAPIRoute(t *testing.T) {
	reqs, _ := loadCollection(t)
	for method, patterns := range apiRoutes(t) {
		for _, pattern := range patterns {
			covered := false
			for _, r := range reqs {
				if r.method == method && matches(pattern, r.path) {
					covered = true
					break
				}
			}
			if !covered {
				t.Errorf("%s /api%s is not in collections/insomnium.json — add a request for it, "+
					"with a description saying its response shape, its status codes and the gotcha "+
					"a client would otherwise hit", method, pattern)
			}
		}
	}
}

// TestCollectionHasNoDeadRequests is the other direction: a request left behind
// after an endpoint moved or was removed. Moving the API under /api (#57) broke
// every path in this file at once.
func TestCollectionHasNoDeadRequests(t *testing.T) {
	reqs, _ := loadCollection(t)
	routes := apiRoutes(t)
	for _, r := range reqs {
		live := false
		for _, pattern := range routes[r.method] {
			if matches(pattern, r.path) {
				live = true
				break
			}
		}
		if !live {
			t.Errorf("collection request %q is %s /api%s, which the router does not serve",
				r.name, r.method, r.path)
		}
	}
}

// TestCollectionRequestsAreDocumented guards the one quality the format can be
// held to mechanically: a request with no description documents nothing.
func TestCollectionRequestsAreDocumented(t *testing.T) {
	reqs, baseURLs := loadCollection(t)
	for _, r := range reqs {
		if r.name == "" {
			t.Errorf("a request for %s %s has no name", r.method, r.path)
		}
		if r.description == "" {
			t.Errorf("request %q (%s %s) has no description, so it documents nothing",
				r.name, r.method, r.path)
		}
	}
	// Every path is written as {{baseUrl}} plus what follows /api, so a baseUrl
	// that does not end in /api would make every comparison here mean something
	// other than it appears to.
	if len(baseURLs) == 0 {
		t.Fatal("no environment in the collection sets baseUrl")
	}
	for _, u := range baseURLs {
		if !strings.HasSuffix(u, "/api") {
			t.Errorf("baseUrl = %q, want it to end in /api", u)
		}
	}
}
