package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// #118 step 3 over the fake host (JD-047): a tenant network, a jail and a
// guest put on it by name, each given the next free address, and the
// network's view naming both.
func TestInstancesOnANetworkAreGivenAddresses(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := send(t, router, http.MethodPost, "/api/networks", cookies, `{"name":"platform"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/networks: %d %s", rec.Code, rec.Body)
	}
	var network struct {
		Bridge  string `json:"bridge"`
		Subnet  string `json:"subnet"`
		Gateway string `json:"gateway"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &network)
	prefix := strings.TrimSuffix(network.Gateway, ".1")

	rec = send(t, router, http.MethodPost, "/api/jails", cookies,
		`{"name":"db","template":"zroot/jails/templates/15.1-RELEASE@base","network":{"mode":"vnet","name":"platform"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/jails: %d %s", rec.Code, rec.Body)
	}
	var jail struct {
		Network string `json:"network"`
		Address string `json:"address"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &jail)
	if jail.Network != "platform" || jail.Address != prefix+".2/24" {
		t.Errorf("jail = %+v; want platform, %s.2/24", jail, prefix)
	}

	rec = send(t, router, http.MethodPost, "/api/vms", cookies, `{"name":"api","image":"debian-13","switch":"platform"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/vms: %d %s", rec.Code, rec.Body)
	}
	var guest struct {
		Network string `json:"network"`
		Address string `json:"address"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &guest)
	if guest.Network != "platform" || guest.Address != prefix+".3/24" {
		t.Errorf("guest = %+v; want platform, %s.3/24", guest, prefix)
	}

	// An address asked for that the jail has is refused, naming it.
	rec = send(t, router, http.MethodPost, "/api/vms", cookies,
		`{"name":"api2","image":"debian-13","switch":"platform","network":{"address":"`+prefix+`.2/24"}}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "in use by jail db") {
		t.Errorf("a taken address: %d %s", rec.Code, rec.Body)
	}

	rec = do(t, router, http.MethodGet, "/api/networks/platform", cookies, nil)
	for _, want := range []string{
		`{"address":"` + prefix + `.2","kind":"jail","name":"db"}`,
		`{"address":"` + prefix + `.3","kind":"guest","name":"api"}`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET /api/networks/platform lacks %s: %s", want, rec.Body)
		}
	}
	rec = do(t, router, http.MethodGet, "/api/vms/api", cookies, nil)
	if !strings.Contains(rec.Body.String(), `"address":"`+prefix+`.3/24"`) {
		t.Errorf("GET /api/vms/api has no address: %s", rec.Body)
	}
	rec = do(t, router, http.MethodGet, "/api/jails/db", cookies, nil)
	if !strings.Contains(rec.Body.String(), `"ip":"`+prefix+`.2"`) {
		t.Errorf("GET /api/jails/db has no ip: %s", rec.Body)
	}
}
