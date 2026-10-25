package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type apiJail struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Path     string `json:"path"`
	Datasets []struct {
		Name       string            `json:"name"`
		Mountpoint string            `json:"mountpoint"`
		Used       int64             `json:"used"`
		Referenced int64             `json:"referenced"`
		Available  int64             `json:"available"`
		Properties map[string]string `json:"properties"`
	} `json:"datasets"`
	Properties map[string]string `json:"properties"`
}

// The acceptance criterion of #2, through the router: GET /api/jails carries
// each jail's datasets and user properties.
func TestJailListingCarriesStorage(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := do(t, router, http.MethodGet, "/api/jails", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/jails returned %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	var jails []apiJail
	if err := json.Unmarshal([]byte(body), &jails); err != nil {
		t.Fatalf("decode jails: %v\n%s", err, body)
	}

	found := map[string]apiJail{}
	for _, jail := range jails {
		found[jail.Name] = jail
	}

	// The fake jail with a path and a child dataset.
	nginx := found["nginx"]
	if len(nginx.Datasets) != 2 {
		t.Errorf("nginx has %d datasets, want its own and its child: %+v", len(nginx.Datasets), nginx.Datasets)
	}
	if nginx.Properties["otter:instance"] != "web-01" {
		t.Errorf("nginx properties = %v, want otter:instance web-01", nginx.Properties)
	}
	// Its own property, not the child's: the child carries otter:retention.
	if _, leaked := nginx.Properties["otter:retention"]; leaked {
		t.Errorf("nginx properties = %v, want only the jail's own dataset's", nginx.Properties)
	}
	if len(nginx.Datasets) > 0 && nginx.Datasets[0].Used == 0 {
		t.Errorf("nginx dataset %+v has no size; the bytes are the point", nginx.Datasets[0])
	}

	// One dataset, no children.
	if redis := found["redis"]; len(redis.Datasets) != 1 || redis.Properties["otter:instance"] != "cache-01" {
		t.Errorf("redis = %+v, want one dataset and otter:instance cache-01", redis)
	}

	// The fake jail with no path: matched to nothing, and both keys left out
	// rather than sent as null.
	if postgres := found["postgres"]; len(postgres.Datasets) != 0 || len(postgres.Properties) != 0 {
		t.Errorf("postgres = %+v, want no storage for a jail with no path", postgres)
	}
	if strings.Contains(body, `"datasets":null`) || strings.Contains(body, `"properties":null`) {
		t.Errorf("response sends null for an absent collection: %s", body)
	}
	// The pool root is mounted "none" and belongs to no jail.
	for _, jail := range jails {
		for _, d := range jail.Datasets {
			if d.Mountpoint == "none" || d.Mountpoint == "-" {
				t.Errorf("%s was given %s, which is mounted nowhere", jail.Name, d.Name)
			}
		}
	}
}

func TestGetOneJail(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	t.Run("by name", func(t *testing.T) {
		rec := do(t, router, http.MethodGet, "/api/jails/nginx", cookies, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/jails/nginx returned %d: %s", rec.Code, rec.Body)
		}
		var jail apiJail
		if err := json.Unmarshal(rec.Body.Bytes(), &jail); err != nil {
			t.Fatalf("decode jail: %v\n%s", err, rec.Body)
		}
		if jail.Name != "nginx" || len(jail.Datasets) != 2 || jail.Properties["otter:instance"] != "web-01" {
			t.Errorf("GET /api/jails/nginx = %+v, want nginx with its storage", jail)
		}
	})

	// chi serves this as the index route of /jails/{name}, so both spellings
	// reach the same handler. A client that builds paths by joining gets one or
	// the other and must not care.
	t.Run("with a trailing slash", func(t *testing.T) {
		rec := do(t, router, http.MethodGet, "/api/jails/nginx/", cookies, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET /api/jails/nginx/ returned %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("statuses", func(t *testing.T) {
		for path, want := range map[string]int{
			"/api/jails/does-not-exist": http.StatusNotFound,
			"/api/jails/bad%20name":     http.StatusBadRequest,
		} {
			if rec := do(t, router, http.MethodGet, path, cookies, nil); rec.Code != want {
				t.Errorf("GET %s returned %d, want %d: %s", path, rec.Code, want, rec.Body)
			}
		}
	})

	t.Run("needs a session", func(t *testing.T) {
		if rec := do(t, router, http.MethodGet, "/api/jails/nginx", nil, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated GET /api/jails/nginx returned %d, want 401", rec.Code)
		}
	})
}

// The acceptance criterion of #55, through the router: a quota set on a child
// comes back on the listing, read from ZFS rather than remembered from the
// request that set it (JD-011).
func TestDatasetsCarryTheirNativeProperties(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	if rec := send(t, router, http.MethodPut, "/api/jails/nginx/datasets/data", cookies,
		`{"properties":{"refquota":"2G","compression":"zstd"}}`); rec.Code != http.StatusCreated {
		t.Fatalf("PUT returned %d, want 201: %s", rec.Code, rec.Body)
	}

	jails := listJails(t, router, cookies)
	var child, root *struct {
		Name       string            `json:"name"`
		Mountpoint string            `json:"mountpoint"`
		Used       int64             `json:"used"`
		Referenced int64             `json:"referenced"`
		Available  int64             `json:"available"`
		Properties map[string]string `json:"properties"`
	}
	for i, dataset := range jails["nginx"].Datasets {
		switch dataset.Name {
		case "zroot/jails/nginx/data":
			child = &jails["nginx"].Datasets[i]
		case "zroot/jails/nginx":
			root = &jails["nginx"].Datasets[i]
		}
	}
	if child == nil || root == nil {
		t.Fatalf("datasets = %+v, want the jail's own and the new child", jails["nginx"].Datasets)
	}

	for name, want := range map[string]string{"refquota": "2G", "compression": "zstd"} {
		if got := child.Properties[name]; got != want {
			t.Errorf("child %s = %q, want %q (all: %v)", name, got, want, child.Properties)
		}
	}

	// The jail's own dataset sets none of them, and says so by carrying
	// nothing rather than a set of ZFS defaults. A default here would read as
	// a value someone chose.
	if len(root.Properties) != 0 {
		t.Errorf("the jail's own dataset reports %v, want nothing: it sets no native property", root.Properties)
	}

	// The jail's tags are still the jail's, not the child's: #55 adds a map
	// beside each dataset and moves nothing (JD-004).
	if jails["nginx"].Properties["otter:instance"] != "web-01" {
		t.Errorf("jail properties = %v, want the tags untouched", jails["nginx"].Properties)
	}
	if _, leaked := child.Properties["otter:instance"]; leaked {
		t.Error("a user property reached a dataset's native map")
	}
}
