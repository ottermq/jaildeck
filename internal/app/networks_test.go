package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTheDefaultNetworkOverTheFakeAdapter(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	type net struct {
		Usable  bool     `json:"usable"`
		Ready   bool     `json:"ready"`
		Changed bool     `json:"changed"`
		Done    []string `json:"done"`
		Parts   []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Owner  string `json:"owner"`
		} `json:"parts"`
	}
	read := func(method string) net {
		t.Helper()
		rec := do(t, router, method, "/api/networks/default", cookies, nil)
		var n net
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &n) != nil {
			t.Fatalf("%s /api/networks/default: %d %s", method, rec.Code, rec.Body)
		}
		return n
	}

	if before := read(http.MethodGet); before.Usable || before.Ready {
		t.Fatalf("a fresh fake host has the network already: %+v", before)
	}
	ensured := read(http.MethodPut)
	if !ensured.Changed || !ensured.Usable || ensured.Ready || len(ensured.Done) != 6 {
		t.Errorf("first PUT = %+v; want Jail Deck's six parts done, usable, not ready (PF is the operator's)", ensured)
	}
	for _, p := range ensured.Parts {
		if p.Owner == "jaildeck" && p.ID != "anchor_loaded" && p.Status != "pass" {
			t.Errorf("Jail Deck's part %s is %s after the PUT", p.ID, p.Status)
		}
	}
	if again := read(http.MethodPut); again.Changed || len(again.Done) != 0 {
		t.Errorf("second PUT changed something: %+v", again)
	}

	// The switch it made is offered to a new guest.
	rec := do(t, router, http.MethodGet, "/api/vms", cookies, nil)
	if !strings.Contains(rec.Body.String(), `"jaildeck"`) {
		t.Errorf("GET /api/vms switches lack the default network's: %s", rec.Body)
	}

	rec = do(t, router, http.MethodGet, "/api/audit?limit=20", cookies, nil)
	for _, op := range []string{"create-vm-switch", "set-vm-switch-private", "enable-ip-forwarding-at-boot", "write-pf-anchor"} {
		if !strings.Contains(rec.Body.String(), `"`+op+`"`) {
			t.Errorf("the audit log has no %s", op)
		}
	}
}

// Tenant networks over the fake host (JD-043): made, listed, refused while a
// guest is on it, destroyed once empty, and the anchor file following.
func TestTenantNetworksOverTheFakeAdapter(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := send(t, router, http.MethodPost, "/api/networks", cookies, `{"name":"acme","tenant":"acme-corp"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"bridge":"jdn0"`) ||
		!strings.Contains(rec.Body.String(), `"subnet":"10.92.0.0/24"`) {
		t.Fatalf("POST /api/networks: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"name":"acme"}`, http.StatusConflict},
		{`{"name":"default"}`, http.StatusBadRequest},
		{`{"name":"acme2","colour":"blue"}`, http.StatusBadRequest},
	} {
		if rec := send(t, router, http.MethodPost, "/api/networks", cookies, tc.body); rec.Code != tc.code {
			t.Errorf("POST %s: %d, want %d: %s", tc.body, rec.Code, tc.code, rec.Body)
		}
	}
	rec = do(t, router, http.MethodGet, "/api/networks", cookies, nil)
	if !strings.Contains(rec.Body.String(), `"range":"10.92.0.0/16"`) || !strings.Contains(rec.Body.String(), `"tenant":"acme-corp"`) {
		t.Errorf("GET /api/networks: %s", rec.Body)
	}
	// The default network is not a tenant network, and keeps its own route.
	if rec := do(t, router, http.MethodGet, "/api/networks/default", cookies, nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"parts"`) {
		t.Errorf("GET /api/networks/default: %d", rec.Code)
	}

	// A guest on the network's switch keeps it.
	guest := `{"name":"web","image":"debian-13","switch":"acme","network":{"address":"10.92.0.10/24","gateway":"10.92.0.1"}}`
	if rec := send(t, router, http.MethodPost, "/api/vms", cookies, guest); rec.Code != http.StatusCreated {
		t.Fatalf("a guest on the network: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodDelete, "/api/networks/acme?confirm=acme", cookies, nil); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "guest web") {
		t.Errorf("DELETE with a guest on it: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodDelete, "/api/vms/web?confirm=web", cookies, nil); rec.Code != http.StatusOK {
		t.Fatalf("destroying the guest: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodDelete, "/api/networks/acme?confirm=acme", cookies, nil); rec.Code != http.StatusOK {
		t.Errorf("DELETE once empty: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodGet, "/api/networks/acme", cookies, nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE: %d", rec.Code)
	}
	rec = do(t, router, http.MethodGet, "/api/audit?limit=30", cookies, nil)
	for _, op := range []string{"create-network-bridge", "set-network-address", "create-vm-switch", "destroy-vm-switch", "destroy-network-bridge"} {
		if !strings.Contains(rec.Body.String(), `"`+op+`"`) {
			t.Errorf("the audit log has no %s", op)
		}
	}
}
