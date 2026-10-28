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
