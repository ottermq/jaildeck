package app

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The generated OpenAPI spec is the API's documentation, and these tests are
// what keep it from drifting away from the router.
//
// swag's weakness is exactly the failure mode that matters here: a handler with
// no annotation is silently skipped, so an undocumented endpoint produces a
// spec that is simply shorter, with nothing anywhere saying so. The first test
// below is what says so. Regenerate with `make doc`.
const specPath = "../../docs/api/swagger.json"

type spec struct {
	BasePath string                                `json:"basePath"`
	Paths    map[string]map[string]specOperation   `json:"paths"`
	Defs     map[string]map[string]json.RawMessage `json:"definitions"`
}

type specOperation struct {
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Responses   map[string]struct {
		Description string `json:"description"`
	} `json:"responses"`
}

func loadSpec(t *testing.T) spec {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read the spec: %v\nRun `make doc`.", err)
	}
	var s spec
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse the spec: %v", err)
	}
	if s.BasePath != "/api" {
		t.Fatalf("basePath = %q, want /api; every path below is compared against it", s.BasePath)
	}
	return s
}

// operations is the spec keyed the way apiRoutes keys the router: method in
// upper case, path with no trailing slash.
func (s spec) operations() map[string]map[string]specOperation {
	found := map[string]map[string]specOperation{}
	for path, methods := range s.Paths {
		for method, op := range methods {
			upper := strings.ToUpper(method)
			if found[upper] == nil {
				found[upper] = map[string]specOperation{}
			}
			found[upper][normalizePath(path)] = op
		}
	}
	return found
}

// normalizePath strips a trailing slash so chi's index routes -- which Walk
// reports as /jails/ and /jails/{name}/ -- compare equal to the spec's /jails
// and /jails/{name}. The empty path stays as it is.
func normalizePath(path string) string {
	if len(path) > 1 {
		return strings.TrimRight(path, "/")
	}
	return path
}

// apiRoutes is every route the router serves under /api, as a path pattern with
// the /api prefix removed. chi and OpenAPI spell a path parameter the same way,
// {name}, so the two are directly comparable.
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

// The one that has caught something in every pull request so far: a route
// added without an annotation.
func TestSpecCoversEveryAPIRoute(t *testing.T) {
	operations := loadSpec(t).operations()
	for method, patterns := range apiRoutes(t) {
		for _, pattern := range patterns {
			if _, ok := operations[method][pattern]; !ok {
				t.Errorf("%s /api%s is in the router but not in the spec — annotate its handler "+
					"with @Router %s [%s] and run `make doc`",
					method, pattern, pattern, strings.ToLower(method))
			}
		}
	}
}

// The other direction: an annotation left behind after an endpoint moved or was
// removed. Moving the API under /api once broke every path at once.
func TestSpecHasNoDeadOperations(t *testing.T) {
	routes := apiRoutes(t)
	for method, operations := range loadSpec(t).operations() {
		for path := range operations {
			if !contains(routes[method], path) {
				t.Errorf("the spec documents %s /api%s, which the router does not serve", method, path)
			}
		}
	}
}

// What the format can be held to mechanically. A summary is what a client sees
// in a list of endpoints; a description with no sentences documents nothing;
// and an operation that names no responses says nothing about what to expect.
func TestEveryOperationIsDocumented(t *testing.T) {
	for method, operations := range loadSpec(t).operations() {
		for path, op := range operations {
			where := method + " /api" + path
			if op.Summary == "" {
				t.Errorf("%s has no @Summary", where)
			}
			if len(op.Description) < 40 {
				t.Errorf("%s has a %d-character description; say what it returns and the gotcha "+
					"a client would otherwise hit", where, len(op.Description))
			}
			if len(op.Responses) == 0 {
				t.Errorf("%s documents no responses", where)
			}
		}
	}
}

// Every failure path a handler can take reaches the client as this shape, so a
// spec that describes some other error body would be wrong about all of them.
func TestErrorsAreDocumentedConsistently(t *testing.T) {
	s := loadSpec(t)
	if _, ok := s.Defs["common.ErrorResponse"]; !ok {
		var names []string
		for name := range s.Defs {
			names = append(names, name)
		}
		sort.Strings(names)
		t.Fatalf("common.ErrorResponse is not in the spec's definitions: %v", names)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// readmeEndpoint is one row of the README's endpoint table: | `METHOD /api/path…` |.
var readmeEndpoint = regexp.MustCompile("(?m)^\\| `(GET|POST|PUT|PATCH|DELETE) /api(/[^` ?]*)")

// The README's table is what a reader scans first. It had fallen thirteen
// endpoints behind -- creating and destroying jails among them -- before this
// test existed.
func TestReadmeListsEveryAPIRoute(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, m := range readmeEndpoint.FindAllStringSubmatch(string(b), -1) {
		listed[m[1]+" "+normalizePath(m[2])] = true
	}
	for method, patterns := range apiRoutes(t) {
		for _, pattern := range patterns {
			if !listed[method+" "+pattern] {
				t.Errorf("%s /api%s is in the router but not in the README's endpoint table", method, pattern)
			}
		}
	}
}
