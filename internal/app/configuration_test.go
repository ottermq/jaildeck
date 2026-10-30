package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// #127 over the fake host (JD-048): a jail's stanza and a guest's files as
// the host has them, the guest's console password hash masked.
func TestTheConfigurationViews(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := send(t, router, http.MethodPost, "/api/jails", cookies,
		`{"name":"db","template":"zroot/jails/templates/15.1-RELEASE@base","limits":{"maxproc":300}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/jails: %d %s", rec.Code, rec.Body)
	}
	rec = send(t, router, http.MethodGet, "/api/jails/db/configuration", cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET jail configuration: %d %s", rec.Code, rec.Body)
	}
	var jail struct {
		File struct {
			Path    string `json:"path"`
			Present bool   `json:"present"`
			Content string `json:"content"`
		} `json:"file"`
		Rules []string `json:"rules"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &jail)
	if !jail.File.Present || !strings.HasSuffix(jail.File.Path, "/db.conf") || !strings.Contains(jail.File.Content, "db {") ||
		jail.Rules == nil {
		t.Errorf("jail = %+v", jail)
	}

	rec = send(t, router, http.MethodPost, "/api/vms", cookies,
		`{"name":"web","image":"debian-13","console_password":"correct horse battery"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/vms: %d %s", rec.Code, rec.Body)
	}
	rec = send(t, router, http.MethodGet, "/api/vms/web/configuration", cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET vm configuration: %d %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); strings.Contains(body, "$2a$") || !strings.Contains(body, `"masked":["password"]`) ||
		!strings.Contains(body, "password: (masked)") {
		t.Errorf("the hash is shown, or the mask is not said:\n%s", body)
	}

	for _, path := range []string{"/api/jails/nosuch/configuration", "/api/vms/nosuch/configuration"} {
		if rec := send(t, router, http.MethodGet, path, cookies, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d", path, rec.Code)
		}
	}
	if rec := send(t, router, http.MethodGet, "/api/vms/web/configuration", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a session: %d", rec.Code)
	}
}
