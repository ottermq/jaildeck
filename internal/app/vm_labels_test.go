package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A guest is labelled through the router, and the listing and its own page
// read the label back from the host (JD-044).
func TestLabelAGuest(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := send(t, router, http.MethodPatch, "/api/vms/jd-cap-stopped/properties", cookies,
		`{"otter:tenant":"beta","otter:tier":"batch"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH returned %d: %s", rec.Code, rec.Body)
	}
	rec = send(t, router, http.MethodPatch, "/api/vms/jd-cap-stopped/properties", cookies, `{"otter:tier":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH removing returned %d: %s", rec.Code, rec.Body)
	}

	rec = do(t, router, http.MethodGet, "/api/vms", cookies, nil)
	var listing struct {
		VMs []struct {
			Name       string            `json:"name"`
			Properties map[string]string `json:"properties"`
		} `json:"vms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	got := map[string]map[string]string{}
	for _, vm := range listing.VMs {
		got[vm.Name] = vm.Properties
	}
	if got["jd-cap-stopped"]["otter:tenant"] != "beta" || len(got["jd-cap-stopped"]) != 1 {
		t.Errorf("jd-cap-stopped = %v, want only otter:tenant=beta", got["jd-cap-stopped"])
	}
	if got["jd-cap-running"]["otter:tenant"] != "acme" {
		t.Errorf("jd-cap-running = %v, want the seeded otter:tenant=acme", got["jd-cap-running"])
	}

	for _, tt := range []struct {
		path, body string
		want       int
	}{
		{"/api/vms/jd-cap-stopped/properties", `{"tenant":"x"}`, http.StatusBadRequest},
		{"/api/vms/nosuch/properties", `{"otter:x":"y"}`, http.StatusNotFound},
		{"/api/vms/jd-cap-stopped/properties", `not json`, http.StatusBadRequest},
	} {
		if rec := send(t, router, http.MethodPatch, tt.path, cookies, tt.body); rec.Code != tt.want {
			t.Errorf("PATCH %s %s returned %d, want %d: %s", tt.path, tt.body, rec.Code, tt.want, rec.Body)
		}
	}
}
