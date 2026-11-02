package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/state"
)

func TestParseGrants(t *testing.T) {
	got, err := ParseGrants([]string{"read", " exec:example:role=cluster ", "read"})
	if err != nil || strings.Join(got, ",") != "read,exec:example:role=cluster" {
		t.Errorf("got %v, %v", got, err)
	}
	for _, bad := range []string{"write", "exec", "exec:role=cluster", "exec:example:role=", "exec:example:role",
		"exec:example:role=a\nb", "console", ""} {
		if _, err := ParseGrants([]string{bad}); err == nil {
			t.Errorf("%q was taken for a grant", bad)
		}
	}
	// A value may itself hold an =.
	if _, err := ParseGrants([]string{"exec:otter:query=a=b"}); err != nil {
		t.Error(err)
	}
}

func TestARestrictedTokenTakesNoScopes(t *testing.T) {
	now := time.Now()
	if _, _, err := AddToken(nil, "worker", []string{"console"}, []string{"read"}, now); !errors.Is(err, ErrScopesOnRestrictedToken) {
		t.Errorf("err = %v", err)
	}
	tokens, _, err := AddToken(nil, "worker", nil, []string{"read"}, now)
	if err != nil || !Restricted(tokens[0]) || tokens[0].Scopes == nil || TokenHas(tokens[0], ScopeConsole) {
		t.Errorf("tokens = %+v, %v", tokens, err)
	}
}

// restrict runs one request through Restrict as the principal in ctx, with
// web carrying example:role=cluster and db example:role=other.
func restrict(t *testing.T, ctx context.Context, method, target string) (int, string) {
	t.Helper()
	properties := func(_ context.Context, jail string) (map[string]string, error) {
		switch jail {
		case "web":
			return map[string]string{"example:role": "cluster"}, nil
		case "db":
			return map[string]string{"example:role": "other"}, nil
		case "broken":
			return nil, errors.New("zfs: cannot open pool")
		}
		return nil, common.NewAppError(http.StatusNotFound, "jail not found")
	}
	h := NewAuthHandler(nil)
	reached := false
	handler := h.Restrict(properties)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil).WithContext(ctx))
	if reached != (rec.Code == http.StatusNoContent) {
		t.Fatalf("reached %v with %d", reached, rec.Code)
	}
	return rec.Code, rec.Body.String()
}

func TestRestrictLetsThroughOnlyWhatTheGrantsName(t *testing.T) {
	as := func(token state.Token) context.Context {
		return context.WithValue(context.Background(), bearerKey{}, token)
	}
	worker := as(state.Token{Name: "worker", Scopes: []string{}, Allow: []string{"read", "exec:example:role=cluster"}})
	cases := []struct {
		name     string
		ctx      context.Context
		method   string
		target   string
		want     int
		mentions string
	}{
		{"a session", context.WithValue(context.Background(), sessionKey{}, Session{}), "DELETE", "/api/jails/db", 204, ""},
		{"a token with the plain API", as(state.Token{Name: "ci", Scopes: []string{}}), "POST", "/api/jails/db/stop", 204, ""},
		{"a read", worker, "GET", "/api/jails", 204, ""},
		{"exec in a cluster jail", worker, "POST", "/api/jails/web/exec", 204, ""},
		{"exec in the platform database", worker, "POST", "/api/jails/db/exec", 403, "has no matching property"},
		{"exec in a jail that is not there", worker, "POST", "/api/jails/nope/exec", 404, "jail not found"},
		{"exec where the properties cannot be read", worker, "POST", "/api/jails/broken/exec", 403, "could not be read"},
		{"a stop", worker, "POST", "/api/jails/web/stop", 403, "may only read; exec in jails with example:role=cluster"},
		{"a relabel", worker, "PATCH", "/api/jails/db/properties", 403, "is neither"},
		{"a destroy", worker, "DELETE", "/api/jails/web", 403, "is neither"},
		{"an escaped path", worker, "POST", "/api/jails/w%65b/exec", 403, "is neither"},
		{"a longer path", worker, "POST", "/api/jails/web/exec/x", 403, "is neither"},
		{"a read without read", as(state.Token{Name: "x", Scopes: []string{}, Allow: []string{"exec:example:role=cluster"}}),
			"GET", "/api/jails", 403, "no read grant"},
		{"exec without exec", as(state.Token{Name: "x", Scopes: []string{}, Allow: []string{"read"}}),
			"POST", "/api/jails/web/exec", 403, "no exec grant"},
		{"a grant this build does not read", as(state.Token{Name: "x", Scopes: []string{}, Allow: []string{"write"}}),
			"GET", "/api/jails", 403, ""},
	}
	for _, c := range cases {
		code, body := restrict(t, c.ctx, c.method, c.target)
		if code != c.want || !strings.Contains(body, c.mentions) {
			t.Errorf("%s: %d %s, want %d mentioning %q", c.name, code, body, c.want, c.mentions)
		}
	}
}

func TestMeAnswersARestrictedTokensGrants(t *testing.T) {
	h := NewAuthHandler(nil)
	rec := httptest.NewRecorder()
	ctx := context.WithValue(context.Background(), bearerKey{}, state.Token{Name: "worker", Scopes: []string{}, Allow: []string{"read"}})
	h.Me(rec, httptest.NewRequest("GET", "/api/auth/me", nil).WithContext(ctx))
	if !strings.Contains(rec.Body.String(), `"allow":["read"]`) || !strings.Contains(rec.Body.String(), `"scopes":[]`) {
		t.Errorf("me = %s", rec.Body)
	}
}
