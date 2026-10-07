package app

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

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

// TestOldAPIPathsNoLongerAnswer will fail once #58 serves index.html for unknown non-/api paths
func TestOldAPIPathsNoLongerAnswer(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	for _, path := range []string{"/jails", "/audit", "/auth/login", "/auth/me"} {
		rec := httptest.NewRecorder()
		a.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s returned %d, want 404", path, rec.Code)
		}
	}
}

func TestAuthenticatedAPIRootRedirectsToAPIJails(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes()
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
