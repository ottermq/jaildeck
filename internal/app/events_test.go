package app

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The live stream through the whole stack (#124, JD-052): RequireAuth and the
// request log in front of it must let it flush, and an operation made through
// the API arrives on it.
func TestAnOperationArrivesOnTheLiveStream(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no login: %v %v, want 401", resp, err)
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: %v %v", resp, err)
	}
	defer resp.Body.Close()
	body := bufio.NewReader(resp.Body)
	// retry: is the first thing written; reading it means the headers and the
	// first flush came through the middleware.
	if line, err := body.ReadString('\n'); err != nil || !strings.HasPrefix(line, "retry:") {
		t.Fatalf("first line %q: %v", line, err)
	}

	if rec := send(t, router, http.MethodPost, "/api/jails/nginx/stop", cookies, ""); rec.Code != http.StatusOK {
		t.Fatalf("stop returned %d: %s", rec.Code, rec.Body)
	}
	for {
		line, err := body.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before the operation: %v", err)
		}
		if strings.HasPrefix(line, "data:") && strings.Contains(line, `"target":"nginx"`) {
			if !strings.Contains(line, `"operation":"stop"`) || !strings.Contains(line, `"success":true`) {
				t.Errorf("event = %s", line)
			}
			return
		}
	}
}
