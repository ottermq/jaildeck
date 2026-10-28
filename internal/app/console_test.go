package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The console through the whole stack -- the router, RequireAuth and the
// request log in front of it -- over the fake adapter (JD-042).
func TestAConsoleThroughTheRouter(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/jails/nginx/console"

	// Without a login: refused before any upgrade.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, resp, err := websocket.Dial(ctx, url, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no login: %v %v, want 401", resp, err)
	}

	header := http.Header{}
	for _, c := range cookies {
		header.Add("Cookie", c.Name+"="+c.Value)
	}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial with the session cookie: %v", err)
	}
	defer conn.CloseNow()
	var out strings.Builder
	for !strings.Contains(out.String(), "root@nginx:/ # ") {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("reading the prompt: %v (%q)", err, out.String())
		}
		out.Write(data)
	}

	// Stopping the jail hangs the console up first.
	if rec := send(t, router, http.MethodPost, "/api/jails/nginx/stop", cookies, ""); rec.Code != http.StatusOK {
		t.Fatalf("stop returned %d: %s", rec.Code, rec.Body)
	}
	reason := ""
	for reason == "" {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for the closed message: %v", err)
		}
		if kind == websocket.MessageText {
			var message struct{ Type, Reason string }
			_ = json.Unmarshal(data, &message)
			reason = message.Reason
		}
	}
	if reason != "jail-stopped" {
		t.Errorf("closed with %q, want jail-stopped", reason)
	}

	// The audit log has the open, the close before the stop, and the stop.
	var entries []struct {
		Operation, Target, Principal, Reason string
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		rec := do(t, router, http.MethodGet, "/api/audit?limit=10", cookies, nil)
		entries = nil
		_ = json.Unmarshal(rec.Body.Bytes(), &entries)
		if len(entries) >= 3 {
			break
		}
	}
	got := make([]string, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		got = append(got, entries[i].Operation+" "+entries[i].Target+" "+entries[i].Principal+" "+entries[i].Reason)
	}
	want := []string{"console-open jail/nginx session ", "console-close jail/nginx session jail-stopped", "stop nginx  "}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("audit, oldest first:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A stopped jail has no console.
	if _, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header}); err == nil || resp.StatusCode != http.StatusConflict {
		t.Errorf("console of a stopped jail: %v %v, want 409", resp, err)
	}
}
