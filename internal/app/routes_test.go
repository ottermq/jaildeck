package app

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestRoutesRequireAuthentication(t *testing.T) {
	publicRoutes := map[string]struct{}{
		"POST /auth/login":  {},
		"POST /auth/logout": {},
	}
	var router http.Handler
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router = a.Routes()
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
