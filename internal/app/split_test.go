package app

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/state"
)

// The principal header is the front's to set (JD-056). One a client sends is
// dropped before the root side sees it, so it neither stands in for
// credentials nor widens a limited token's.
func TestAForgedPrincipalDoesNotCross(t *testing.T) {
	const plain = "jd_split_test_token"
	a, err := newTestAppWith(t, nil, []state.Token{{
		Name: "worker", Hash: auth.HashToken(plain), CreatedAt: time.Now(),
		Scopes: []string{}, Allow: []string{"read"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	router := a.Routes(testAssets())
	session := base64.RawURLEncoding.EncodeToString([]byte(`{"kind":"session"}`))

	forged := map[string]string{auth.PrincipalHeader: session}
	if rec := sendWithHeaders(t, router, "POST", "/api/jails/nginx/stop", nil, "", forged); rec.Code != http.StatusUnauthorized {
		t.Errorf("no credentials: %d %s, want 401", rec.Code, rec.Body)
	}

	forged["Authorization"] = "Bearer " + plain
	if rec := sendWithHeaders(t, router, "POST", "/api/jails/nginx/stop", nil, "", forged); rec.Code != http.StatusForbidden {
		t.Errorf("limited token: %d %s, want 403", rec.Code, rec.Body)
	}
}

// The root side serves nothing without the front's principal: a request
// that reached its socket some other way is unauthenticated.
func TestTheRootSideWantsAPrincipal(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/jails", "/metrics"} {
		rec := httptest.NewRecorder()
		a.back.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", path, rec.Code)
		}
	}
}
