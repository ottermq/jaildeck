package app

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// The Prometheus endpoint, through the real router: the bare /metrics a
// scraper looks for, behind the same authentication as everything else
// (JD-033).
//
// TestRoutesRequireAuthentication already walks every route and demands a 401
// without a session, so the unauthenticated half is covered there. This is
// the other half -- that it answers at all, in the right format, at the path
// a scrape config will name.
func TestPrometheusEndpoint(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	rec := do(t, router, http.MethodGet, "/metrics", login(t, router), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics returned %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	// Rendered whole before anything is sent, so the length is known. A
	// scraper that reads a truncated body reads it as a successful scrape of
	// a host that lost half its disks.
	length, err := strconv.Atoi(rec.Header().Get("Content-Length"))
	if err != nil || length != rec.Body.Len() {
		t.Errorf("Content-Length = %q, body is %d bytes",
			rec.Header().Get("Content-Length"), rec.Body.Len())
	}

	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE jaildeck_host_load1 gauge",
		"# TYPE jaildeck_host_cpu_ticks_total counter",
		"jaildeck_jails_configured ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body carries no %q", want)
		}
	}
}

// The JSON report stays where it was. The two answer the same question for
// different readers, and neither moved to make room for the other.
func TestTheJSONReportIsStillUnderAPI(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := do(t, router, http.MethodGet, "/api/metrics", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/metrics returned %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", got)
	}
	if body := rec.Body.String(); !strings.HasPrefix(strings.TrimSpace(body), "{") {
		t.Errorf("/api/metrics no longer answers JSON: %.60s", body)
	}
}

// /metrics used to fall through to the SPA, which answers 200 and an HTML
// document to everything it does not recognise. A scraper given that reports
// a parse failure rather than an unreachable target, which is the harder
// thing to debug.
func TestMetricsIsNotServedBySPAFallback(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	body := do(t, router, http.MethodGet, "/metrics", login(t, router), nil).Body.String()
	if strings.Contains(strings.ToLower(body), "<!doctype html") {
		t.Error("/metrics returned the SPA document")
	}
}
