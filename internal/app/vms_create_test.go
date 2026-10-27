package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCreateAndDestroyAVMOverTheFakeAdapter(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	spec := `{"name":"web","image":"debian-13","cpu":2,"memory":"1G","disk":"8G","switch":"jd-capsw",
		"network":{"address":"10.77.0.10/24","gateway":"10.77.0.1","nameservers":["10.77.0.1"]},
		"ssh_keys":["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFMz6uwq6uCySEimJJLIKB1qaSuK3GpjZ0BSxasBIfDG jd-capture"],
		"start":true}`
	rec := send(t, router, http.MethodPost, "/api/vms", cookies, spec)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/vms returned %d: %s", rec.Code, rec.Body)
	}
	var created struct {
		Name  string `json:"name"`
		State string `json:"state"`
		CPU   string `json:"cpu"`
		Image string `json:"image"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "web" || created.State != "running" || created.CPU != "2" || created.Image != "debian-13" {
		t.Errorf("created = %+v", created)
	}

	// Its page has the NIC on the switch and the disk at its size.
	rec = do(t, router, http.MethodGet, "/api/vms/web", cookies, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"switch":"jd-capsw"`) ||
		!strings.Contains(rec.Body.String(), `"size":8589934592`) {
		t.Errorf("GET /api/vms/web: %d %s", rec.Code, rec.Body)
	}

	for _, tc := range []struct {
		body string
		code int
	}{
		{spec, http.StatusConflict},
		{`{"name":"db","image":"ubuntu"}`, http.StatusBadRequest},
		{`{"name":"db","image":"debian-13","switch":"public"}`, http.StatusBadRequest},
		{`{"name":"db","image":"debian-13","colour":"blue"}`, http.StatusBadRequest},
	} {
		if rec := send(t, router, http.MethodPost, "/api/vms", cookies, tc.body); rec.Code != tc.code {
			t.Errorf("POST %s returned %d, want %d: %s", tc.body, rec.Code, tc.code, rec.Body)
		}
	}

	// Running, so refused; stopped, destroyed.
	if rec := do(t, router, http.MethodDelete, "/api/vms/web?confirm=web", cookies, nil); rec.Code != http.StatusConflict {
		t.Errorf("DELETE of a running guest returned %d, want 409", rec.Code)
	}
	if rec := send(t, router, http.MethodPost, "/api/vms/web/stop", cookies, ""); rec.Code != http.StatusOK {
		t.Fatalf("stop returned %d: %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodDelete, "/api/vms/web?confirm=web", cookies, nil); rec.Code != http.StatusOK {
		t.Errorf("DELETE returned %d, want 200: %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodGet, "/api/vms/web", cookies, nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE returned %d, want 404", rec.Code)
	}

	rec = do(t, router, http.MethodGet, "/api/audit?limit=20", cookies, nil)
	for _, operation := range []string{"vm-create", "vm-add-network", "vm-replace-disk", "vm-start", "vm-destroy"} {
		if !strings.Contains(rec.Body.String(), `"`+operation+`"`) {
			t.Errorf("the audit log has no %s", operation)
		}
	}
}
