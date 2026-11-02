package events

import (
	"bufio"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func server(t *testing.T, b *Broker, recheck func(*http.Request) error, heartbeat time.Duration) *httptest.Server {
	t.Helper()
	h := NewHandler(b, recheck)
	h.heartbeat = heartbeat
	r := chi.NewRouter()
	RegisterEventRoutes(r, h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func open(t *testing.T, url, lastID string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url+"/events", nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body)
}

// frame reads one SSE frame, skipping comments and the retry line.
func frame(t *testing.T, r *bufio.Reader) map[string]string {
	t.Helper()
	out := map[string]string{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended: %v (so far %v)", err, out)
		}
		line = strings.TrimRight(line, "\n")
		if line == "" {
			if out["event"] != "" {
				return out
			}
			continue
		}
		if strings.HasPrefix(line, ":") || strings.HasPrefix(line, "retry:") {
			continue
		}
		k, v, _ := strings.Cut(line, ": ")
		out[k] = v
	}
}

func TestTheStreamCarriesEventsAndCatchesUp(t *testing.T) {
	b := NewBroker()
	srv := server(t, b, func(*http.Request) error { return nil }, time.Minute)
	resp, r := open(t, srv.URL, "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for b.Streams() == 0 {
		time.Sleep(time.Millisecond)
	}
	e := b.Publish(TypeOperation, Operation{Operation: "stop", Target: "web", Success: true})
	f := frame(t, r)
	if f["id"] != e.ID || f["event"] != "operation" || !strings.Contains(f["data"], `"target":"web"`) {
		t.Errorf("frame = %v", f)
	}

	b.Publish(TypeProgress, Progress{Kind: "image", Name: "noble", Step: "writing"})
	_, again := open(t, srv.URL, e.ID)
	if f := frame(t, again); f["event"] != "progress" || !strings.Contains(f["data"], `"step":"writing"`) {
		t.Errorf("replay = %v", f)
	}
	_, stale := open(t, srv.URL, "00000000-3")
	if f := frame(t, stale); f["event"] != "reset" || !strings.HasPrefix(f["id"], b.boot+"-") {
		t.Errorf("stale reconnect = %v", f)
	}
}

func TestAStreamEndsWhenItsCredentialsDoNot(t *testing.T) {
	var revoked atomic.Bool
	b := NewBroker()
	srv := server(t, b, func(*http.Request) error {
		if revoked.Load() {
			return errors.New("token revoked")
		}
		return nil
	}, 20*time.Millisecond)
	_, r := open(t, srv.URL, "")
	revoked.Store(true)
	done := make(chan error, 1)
	go func() {
		_, err := r.ReadString(0)
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream stayed open after its token was revoked")
	}
}

func TestStreamsPastTheCapAreRefused(t *testing.T) {
	b := NewBroker()
	for range MaxStreams {
		s, _, _, _, _ := b.Subscribe("")
		defer b.Unsubscribe(s)
	}
	srv := server(t, b, func(*http.Request) error { return nil }, time.Minute)
	if resp, _ := open(t, srv.URL, ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
