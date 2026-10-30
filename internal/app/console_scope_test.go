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
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/state"
)

// Who may open a console, through the whole stack (#155, #178): a token made
// before scopes, one with the console scope, one without; the browser session
// with JAILDECK_CONSOLE_UI on and off.
func TestWhoMayOpenAConsole(t *testing.T) {
	tokens := []state.Token{
		{Name: "old", Hash: auth.HashToken("jd_old"), CreatedAt: time.Unix(1, 0)},
		{Name: "otter", Hash: auth.HashToken("jd_otter"), CreatedAt: time.Unix(2, 0), Scopes: []string{"console"}},
		{Name: "ci", Hash: auth.HashToken("jd_ci"), CreatedAt: time.Unix(3, 0), Scopes: []string{}},
	}
	for _, uiOff := range []bool{false, true} {
		a, err := newTestAppWith(t, func(c *config.Config) { c.NoConsoleUI = uiOff }, tokens)
		if err != nil {
			t.Fatalf("create test app: %v", err)
		}
		router := a.Routes(testAssets())
		cookies := login(t, router)
		server := httptest.NewServer(router)
		t.Cleanup(server.Close)
		base := "ws" + strings.TrimPrefix(server.URL, "http")

		session := http.Header{}
		for _, c := range cookies {
			session.Add("Cookie", c.Name+"="+c.Value)
		}
		bearer := func(token string) http.Header { return http.Header{"Authorization": {"Bearer " + token}} }
		sessionWant := http.StatusSwitchingProtocols
		if uiOff {
			sessionWant = http.StatusForbidden
		}
		for _, tc := range []struct {
			who    string
			header http.Header
			want   int
		}{
			{"session", session, sessionWant},
			{"old token", bearer("jd_old"), http.StatusSwitchingProtocols},
			{"console token", bearer("jd_otter"), http.StatusSwitchingProtocols},
			{"token without the scope", bearer("jd_ci"), http.StatusForbidden},
		} {
			for _, path := range []string{"/api/jails/nginx/console", "/api/vms/jd-cap-running/console"} {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				conn, resp, err := websocket.Dial(ctx, base+path, &websocket.DialOptions{HTTPHeader: tc.header})
				got := 0
				if resp != nil {
					got = resp.StatusCode
				}
				if got != tc.want {
					t.Errorf("console UI off=%t, %s, %s: %d (%v), want %d", uiOff, tc.who, path, got, err, tc.want)
				}
				if conn != nil {
					_ = conn.Close(websocket.StatusNormalClosure, "")
				}
				cancel()
			}
		}

		// The plain API stays open to the token without the scope.
		rec := do(t, router, http.MethodGet, "/api/jails", nil, map[string]string{"Authorization": "Bearer jd_ci"})
		if rec.Code != http.StatusOK {
			t.Errorf("GET /api/jails with a scopeless token: %d, want 200", rec.Code)
		}
	}
}
