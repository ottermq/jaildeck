package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/state"
)

// A restricted token through the whole stack (JD-055): it reads, it runs
// commands in the jails its grant names, and it reaches nothing else -- not
// another jail, not a lifecycle operation, not the properties that would let
// it widen its own grant, not the console.
func TestARestrictedTokenThroughTheRouter(t *testing.T) {
	const plain = "jd_restricted_test_token"
	a, err := newTestAppWith(t, nil, []state.Token{{
		Name: "worker", Hash: auth.HashToken(plain), CreatedAt: time.Now(),
		Scopes: []string{}, Allow: []string{"read", "exec:otter:tier=frontend"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	router := a.Routes(testAssets())
	bearer := map[string]string{"Authorization": "Bearer " + plain}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		return sendWithHeaders(t, router, method, path, nil, body, bearer)
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/jails", "", http.StatusOK},
		{"GET", "/api/jails/redis", "", http.StatusOK},
		{"GET", "/metrics", "", http.StatusOK},
		{"GET", "/api/auth/me", "", http.StatusOK},
		{"POST", "/api/jails/nginx/exec", `{"argv":["hostname"]}`, http.StatusOK},
		{"POST", "/api/jails/redis/exec", `{"argv":["hostname"]}`, http.StatusForbidden},
		{"POST", "/api/jails/nope/exec", `{"argv":["hostname"]}`, http.StatusNotFound},
		{"POST", "/api/jails/nginx/stop", "", http.StatusForbidden},
		{"PATCH", "/api/jails/redis/properties", `{"otter:tier":"frontend"}`, http.StatusForbidden},
		{"PUT", "/api/jails/nginx/snapshots/x", "", http.StatusForbidden},
		{"DELETE", "/api/vms/x?confirm=x", "", http.StatusForbidden},
	} {
		if rec := call(c.method, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s %s: %d %s, want %d", c.method, c.path, rec.Code, rec.Body, c.want)
		}
	}
	if rec := call("GET", "/api/auth/me", ""); !strings.Contains(rec.Body.String(), `"allow":["read","exec:otter:tier=frontend"]`) {
		t.Errorf("me = %s", rec.Body)
	}

	// The console is a GET, and read does not open it: a restricted token
	// has no scopes.
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{"Authorization": {"Bearer " + plain}}
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/jails/nginx/console",
		&websocket.DialOptions{HTTPHeader: header})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("console: %v %v, want 403", resp, err)
	}
}
