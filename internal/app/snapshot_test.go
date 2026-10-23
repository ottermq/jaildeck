package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

type apiSnapshotResult struct {
	Snapshot  string   `json:"snapshot"`
	Created   []string `json:"created"`
	Destroyed []string `json:"destroyed"`
	Existed   bool     `json:"existed"`
	Blocked   *struct {
		Reason string   `json:"reason"`
		Newer  []string `json:"newer"`
		Clones []string `json:"clones"`
		Holds  []string `json:"holds"`
	} `json:"blocked"`
}

func snapshotCall(t *testing.T, router http.Handler, cookies []*http.Cookie, method, path string) (int, apiSnapshotResult) {
	t.Helper()
	rec := do(t, router, method, path, cookies, nil)
	var result apiSnapshotResult
	if rec.Code < 400 || rec.Code == http.StatusConflict {
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatalf("%s %s: decode %v\n%s", method, path, err, rec.Body)
		}
	}
	return rec.Code, result
}

// A jail-level snapshot takes the jail and its children at one point in time.
func TestSnapshotAJailRecursively(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	code, result := snapshotCall(t, router, cookies, http.MethodPut, "/api/jails/nginx/snapshots/checkpoint")
	if code != http.StatusCreated {
		t.Fatalf("PUT returned %d, want 201", code)
	}
	want := []string{"zroot/jails/nginx@checkpoint", "zroot/jails/nginx/logs@checkpoint"}
	if len(result.Created) != len(want) {
		t.Errorf("Created = %q, want the jail and its child: %q", result.Created, want)
	}

	// Repeating it is the retry after a timeout, not a failure.
	code, result = snapshotCall(t, router, cookies, http.MethodPut, "/api/jails/nginx/snapshots/checkpoint")
	if code != http.StatusOK || !result.Existed || len(result.Created) != 0 {
		t.Errorf("second PUT = %d %+v, want 200 existed with nothing created", code, result)
	}

	// A child route takes that dataset alone.
	code, result = snapshotCall(t, router, cookies, http.MethodPut, "/api/jails/nginx/datasets/logs/snapshots/logs-only")
	if code != http.StatusCreated || len(result.Created) != 1 {
		t.Errorf("child PUT = %d %+v, want 201 with exactly one snapshot", code, result)
	}
}

func TestListSnapshots(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	rec := do(t, router, http.MethodGet, "/api/jails/nginx/snapshots", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET snapshots returned %d: %s", rec.Code, rec.Body)
	}
	var snapshots []struct {
		Name      string `json:"name"`
		Dataset   string `json:"dataset"`
		Short     string `json:"short"`
		CreateTXG uint64 `json:"create_txg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshots); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	if len(snapshots) == 0 {
		t.Fatal("no snapshots, want the seeded ones")
	}
	// Ordered by transaction group, which is the only exact ordering.
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i].CreateTXG < snapshots[i-1].CreateTXG {
			t.Errorf("snapshot %d is older than %d, want them ordered by create_txg", i, i-1)
		}
	}
	// A jail's listing covers its children's snapshots too.
	datasets := map[string]bool{}
	for _, snapshot := range snapshots {
		datasets[snapshot.Dataset] = true
	}
	if !datasets["zroot/jails/nginx"] || !datasets["zroot/jails/nginx/logs"] {
		t.Errorf("datasets = %v, want the jail and its child", datasets)
	}
}

// The acceptance criterion, through the router.
func TestRollbackRefusesThenObeys(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	const confirm = "confirm=zroot/jails/nginx@before-upgrade"
	base := "/api/jails/nginx/snapshots/before-upgrade/rollback?"

	code, result := snapshotCall(t, router, cookies, http.MethodPost, base+confirm)
	if code != http.StatusConflict {
		t.Fatalf("rollback returned %d, want 409: %+v", code, result)
	}
	if result.Blocked == nil || len(result.Blocked.Newer) == 0 {
		t.Fatalf("blocked = %+v, want it to name the newer snapshots", result.Blocked)
	}
	for _, name := range result.Blocked.Newer {
		if name == "zroot/jails/nginx/logs@before-upgrade" {
			t.Error("a child's snapshot was named; zfs rollback never touches another dataset")
		}
	}

	// Told explicitly, it is still refused -- and for a different reason. The
	// newer snapshot it would have to destroy is held, and a hold cannot be
	// overridden here at any level of insistence: releasing one is a separate,
	// deliberate act. zfs would refuse too; this refuses before running it.
	code, result = snapshotCall(t, router, cookies, http.MethodPost, base+confirm+"&destroy_newer=true")
	if code != http.StatusConflict {
		t.Fatalf("rollback with destroy_newer returned %d, want 409 for the held snapshot: %+v", code, result)
	}
	if result.Blocked == nil || len(result.Blocked.Holds) == 0 {
		t.Errorf("blocked = %+v, want it to name the hold rather than the newer snapshots", result.Blocked)
	}

	// On a dataset whose newer snapshots are not held, it goes ahead.
	const childConfirm = "confirm=zroot/jails/nginx/logs@before-upgrade"
	if code, _ := snapshotCall(t, router, cookies, http.MethodPut,
		"/api/jails/nginx/datasets/logs/snapshots/later"); code != http.StatusCreated {
		t.Fatalf("could not take a newer snapshot on the child: %d", code)
	}
	childBase := "/api/jails/nginx/datasets/logs/snapshots/before-upgrade/rollback?"

	code, result = snapshotCall(t, router, cookies, http.MethodPost, childBase+childConfirm)
	if code != http.StatusConflict || result.Blocked == nil {
		t.Fatalf("child rollback = %d %+v, want 409 naming the newer snapshot", code, result)
	}
	code, result = snapshotCall(t, router, cookies, http.MethodPost, childBase+childConfirm+"&destroy_newer=true")
	if code != http.StatusOK {
		t.Fatalf("child rollback with destroy_newer returned %d, want 200: %+v", code, result)
	}
	if len(result.Destroyed) != 1 || result.Destroyed[0] != "zroot/jails/nginx/logs@later" {
		t.Errorf("Destroyed = %q, want exactly the newer snapshot", result.Destroyed)
	}
}

func TestDestroySnapshotRefusals(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	t.Run("a hold", func(t *testing.T) {
		code, result := snapshotCall(t, router, cookies, http.MethodDelete,
			"/api/jails/nginx/snapshots/nightly?confirm=zroot/jails/nginx@nightly")
		if code != http.StatusConflict {
			t.Fatalf("DELETE returned %d, want 409: %+v", code, result)
		}
		if result.Blocked == nil || len(result.Blocked.Holds) == 0 {
			t.Errorf("blocked = %+v, want it to name the hold", result.Blocked)
		}
	})

	t.Run("a dependent clone", func(t *testing.T) {
		code, result := snapshotCall(t, router, cookies, http.MethodDelete,
			"/api/jails/redis/snapshots/cloned-from?confirm=zroot/jails/redis@cloned-from")
		if code != http.StatusConflict {
			t.Fatalf("DELETE returned %d, want 409: %+v", code, result)
		}
		if result.Blocked == nil || len(result.Blocked.Clones) == 0 {
			t.Errorf("blocked = %+v, want it to name the clone", result.Blocked)
		}
	})

	t.Run("confirm must be the full name", func(t *testing.T) {
		for _, query := range []string{"", "?confirm=before-upgrade", "?confirm=zroot/jails/redis@before-upgrade"} {
			rec := do(t, router, http.MethodDelete, "/api/jails/nginx/snapshots/before-upgrade"+query, cookies, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("DELETE %q returned %d, want 400: %s", query, rec.Code, rec.Body)
			}
		}
	})

	t.Run("and then it works", func(t *testing.T) {
		code, result := snapshotCall(t, router, cookies, http.MethodDelete,
			"/api/jails/nginx/snapshots/before-upgrade?confirm=zroot/jails/nginx@before-upgrade")
		if code != http.StatusOK {
			t.Fatalf("DELETE returned %d, want 200: %+v", code, result)
		}
		// Recursive, like the create: the child's snapshot of the same name
		// goes with it.
		if len(result.Destroyed) != 2 {
			t.Errorf("Destroyed = %q, want the jail's and its child's", result.Destroyed)
		}
	})
}

func TestSnapshotsAreAudited(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("create test app: %v", err)
	}
	router := a.Routes(testAssets())
	cookies := login(t, router)

	req := do(t, router, http.MethodPut, "/api/jails/nginx/snapshots/audited", cookies,
		map[string]string{"X-Request-Id": "otter.job.4"})
	if req.Code != http.StatusCreated {
		t.Fatalf("PUT returned %d: %s", req.Code, req.Body)
	}
	entries := auditEntries(t, router, cookies, "?request_id=otter.job.4")
	if len(entries) != 1 || entries[0].Operation != "create-snapshot" {
		t.Fatalf("audit = %+v, want one create-snapshot", entries)
	}
	if entries[0].Target != "zroot/jails/nginx@audited" {
		t.Errorf("target = %q, want the snapshot's full name", entries[0].Target)
	}
}
