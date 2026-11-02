package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/state"
)

// What the root side rebuilds is what the front authenticated (JD-056),
// with a nil list of scopes kept apart from an empty one: nil is a token
// made before scopes, which has every one.
func TestTheForwardedPrincipalRoundTrips(t *testing.T) {
	expires := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		ctx  context.Context
		want func(t *testing.T, ctx context.Context)
	}{
		{"legacy token", context.WithValue(context.Background(), bearerKey{}, state.Token{Name: "old", Hash: "h"}),
			func(t *testing.T, ctx context.Context) {
				token, ok := tokenFromContext(ctx)
				if !ok || token.Name != "old" || token.Scopes != nil || token.Hash != "" || !TokenHas(token, ScopeConsole) {
					t.Errorf("token = %+v", token)
				}
			}},
		{"limited token", context.WithValue(context.Background(), bearerKey{},
			state.Token{Name: "worker", Scopes: []string{}, Allow: []string{"read", "exec:example:role=a,b"}}),
			func(t *testing.T, ctx context.Context) {
				token, _ := tokenFromContext(ctx)
				if token.Scopes == nil || TokenHas(token, ScopeConsole) || !slices.Equal(token.Allow, []string{"read", "exec:example:role=a,b"}) {
					t.Errorf("token = %+v", token)
				}
			}},
		{"session", context.WithValue(context.Background(), sessionKey{}, Session{ExpiresAt: expires}),
			func(t *testing.T, ctx context.Context) {
				session, ok := SessionFromContext(ctx)
				if !ok || !session.ExpiresAt.Equal(expires) {
					t.Errorf("session = %+v, %v", session, ok)
				}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/jails", nil)
			req.Header.Set("X-Jaildeck-Principal", "forged")
			req.Header.Set("X-Jaildeck-Other", "forged")
			ForwardPrincipal(c.ctx, req.Header)
			if req.Header.Get("X-Jaildeck-Other") != "" {
				t.Error("an X-Jaildeck- header was passed on")
			}
			reached := false
			NewAuthHandler(nil).Trusted(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				reached = true
				c.want(t, r.Context())
			})).ServeHTTP(httptest.NewRecorder(), req)
			if !reached {
				t.Fatal("Trusted refused the front's own principal")
			}
		})
	}
}

func TestTrustedRefusesWhatTheFrontDidNotWrite(t *testing.T) {
	for _, value := range []string{"", "not base64!", "e30", "eyJraW5kIjoidG9rZW4ifQ"} {
		req := httptest.NewRequest(http.MethodGet, "/api/jails", nil)
		if value != "" {
			req.Header.Set(PrincipalHeader, value)
		}
		rec := httptest.NewRecorder()
		NewAuthHandler(nil).Trusted(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Errorf("%q reached the handler", value)
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%q: %d, want 401", value, rec.Code)
		}
	}
}
