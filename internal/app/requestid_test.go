package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// login returns the cookies a session needs, so these tests exercise the same
// path a browser or an automation client does rather than calling a service.
func login(t *testing.T, router http.Handler) []*http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"test-password"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login returned %d: %s", rec.Code, rec.Body)
	}
	return rec.Result().Cookies()
}

func do(t *testing.T, router http.Handler, method, path string, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

type auditEntry struct {
	Operation string `json:"operation"`
	Target    string `json:"target"`
	RequestID string `json:"request_id"`
}

func auditEntries(t *testing.T, router http.Handler, cookies []*http.Cookie, query string) []auditEntry {
	t.Helper()
	rec := do(t, router, http.MethodGet, "/api/audit"+query, cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/audit%s returned %d: %s", query, rec.Code, rec.Body)
	}
	var entries []auditEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode audit entries: %v", err)
	}
	return entries
}

// The whole chain: a header on a mutating request reaches the audit entry that
// request produced, and ?request_id= finds it again (#14, JD-016).
func TestRequestIDReachesTheAuditLog(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if rec := do(t, router, http.MethodPost, "/api/jails/nginx/stop", cookies,
		map[string]string{"X-Request-Id": "otter.job.42"}); rec.Code != http.StatusOK {
		t.Fatalf("stop returned %d: %s", rec.Code, rec.Body)
	}

	entries := auditEntries(t, router, cookies, "?request_id=otter.job.42")
	if len(entries) != 1 {
		t.Fatalf("?request_id=otter.job.42 returned %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].Target != "nginx" || entries[0].RequestID != "otter.job.42" {
		t.Errorf("entry = %+v, want target nginx with request_id otter.job.42", entries[0])
	}

	if got := auditEntries(t, router, cookies, "?request_id=otter.job.43"); len(got) != 0 {
		t.Errorf("an id nothing was logged under returned %d entries, want 0", len(got))
	}
}

// An invalid id is dropped, not rejected: the operation still succeeds and the
// entry simply carries no id, which is the shape a client that sent none gets.
func TestInvalidRequestIDIsDroppedNotRejected(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	// Two shapes only: the exhaustive charset and length table is
	// TestValidateRequestID, and the fake adapter sleeps 3s per operation on
	// purpose, so each extra value here costs `make check` three seconds.
	for _, bad := range []string{"otter job", strings.Repeat("x", 65)} {
		rec := do(t, router, http.MethodPost, "/api/jails/nginx/start", cookies,
			map[string]string{"X-Request-Id": bad})
		if rec.Code != http.StatusOK {
			t.Fatalf("start with id %q returned %d, want the operation to succeed anyway: %s", bad, rec.Code, rec.Body)
		}
	}

	for _, e := range auditEntries(t, router, cookies, "") {
		if e.RequestID != "" {
			t.Errorf("entry %+v carries a request id, want an invalid one dropped", e)
		}
	}
}

// A request that sets no header leaves the field out of the JSON entirely,
// rather than writing "request_id":"".
func TestNoRequestIDLeavesTheFieldOut(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if rec := do(t, router, http.MethodPost, "/api/jails/nginx/restart", cookies, nil); rec.Code != http.StatusOK {
		t.Fatalf("restart returned %d: %s", rec.Code, rec.Body)
	}
	rec := do(t, router, http.MethodGet, "/api/audit", cookies, nil)
	if strings.Contains(rec.Body.String(), "request_id") {
		t.Errorf("audit response = %s, want no request_id field", rec.Body)
	}
}
