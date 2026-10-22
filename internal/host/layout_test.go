package host

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

// noZFS is what zpool list returns on a host without ZFS: the binary exists
// and exits 1 (testdata/hostfacts/rivendell/zpool_list_h).
func noZFS() error {
	return &system.CommandError{
		Command: "zpool",
		Args:    []string{"list", "-H", "-o", "name"},
		Result:  system.CommandResult{ExitCode: 1, Stderr: "Failed to load zfs module: Operation not permitted\n"},
		Err:     errors.New("exit status 1"),
	}
}

func TestZFSPoolCheck(t *testing.T) {
	tests := []struct {
		name         string
		pools        []string
		poolsErr     error
		configured   string
		want         CheckStatus
		wantObserved string
		wantRemedy   string
		wantDetail   string
	}{
		{name: "one pool is adopted", pools: []string{"zroot"}, want: CheckPass, wantObserved: "zroot"},
		// isengard: backup is printed first and holds a replica mounted nowhere.
		{name: "several pools, none configured", pools: []string{"backup", "tank", "zroot"}, want: CheckFail,
			wantObserved: "backup, tank, zroot", wantRemedy: "JAILDECK_ZFS_POOL", wantDetail: "does not guess"},
		{name: "configured pool present", pools: []string{"backup", "tank", "zroot"}, configured: "tank",
			want: CheckPass, wantObserved: "tank"},
		{name: "configured pool absent", pools: []string{"zroot"}, configured: "tank", want: CheckFail,
			wantObserved: "zroot", wantRemedy: "one of: zroot", wantDetail: `"tank"`},
		{name: "ZFS loaded, no pool", pools: []string{}, want: CheckFail, wantRemedy: "zpool create"},
		{name: "no ZFS at all", poolsErr: noZFS(), want: CheckFail,
			wantObserved: "Failed to load zfs module: Operation not permitted", wantRemedy: "kldload zfs", wantDetail: "JD-006"},
		{name: "zpool timed out", poolsErr: context.DeadlineExceeded, want: CheckUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := readyHost()
			system.pools, system.poolsErr = tt.pools, tt.poolsErr
			report, err := NewService(system, &fakeLogger{}, tt.configured).Readiness(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			c := checkByID(t, report, CheckZFSPool)
			if c.Status != tt.want || c.Observed != tt.wantObserved {
				t.Errorf("zfs_pool = %s %q, want %s %q (detail %q)", c.Status, c.Observed, tt.want, tt.wantObserved, c.Detail)
			}
			if (c.Remedy != "") != (c.Status == CheckFail) || !strings.Contains(c.Remedy, tt.wantRemedy) {
				t.Errorf("remedy = %q, want it to contain %q", c.Remedy, tt.wantRemedy)
			}
			if !strings.Contains(c.Detail, tt.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", c.Detail, tt.wantDetail)
			}
			if c.Status != CheckPass {
				if layout := checkByID(t, report, CheckZFSLayout); layout.Status != CheckUnknown {
					t.Errorf("zfs_layout = %s with no pool to look on, want unknown", layout.Status)
				}
			}
		})
	}
}

func TestZFSLayoutCheck(t *testing.T) {
	tests := []struct {
		name         string
		datasets     []Dataset
		datasetsErr  error
		want         CheckStatus
		wantObserved string
		wantRemedy   string
	}{
		{name: "complete", datasets: fullLayout("tank"), want: CheckPass, wantObserved: "tank/jails at /usr/local/jails"},
		{name: "partly there", want: CheckFail, wantRemedy: "jaildeck init",
			datasets: []Dataset{
				{Name: "tank", Mountpoint: "none"},
				{Name: "tank/jails", Mountpoint: "/usr/local/jails"},
				{Name: "tank/jails/templates", Mountpoint: "/usr/local/jails/templates"},
			},
			wantObserved: "missing tank/jails/media, tank/jails/containers"},
		{name: "nothing provisioned", datasets: []Dataset{{Name: "tank", Mountpoint: "none"}}, want: CheckFail,
			wantObserved: "missing tank/jails, tank/jails/media, tank/jails/templates, tank/jails/containers"},
		{name: "parent mounted elsewhere", want: CheckFail,
			datasets:     []Dataset{{Name: "tank", Mountpoint: "none"}, {Name: "tank/jails", Mountpoint: "none"}},
			wantObserved: "tank/jails is mounted at none, expected /usr/local/jails",
			wantRemedy:   "zfs set mountpoint=/usr/local/jails tank/jails"},
		{name: "child mounted elsewhere", want: CheckFail,
			datasets: func() []Dataset {
				d := fullLayout("tank")
				d[4].Mountpoint = "/srv/media"
				return d
			}(),
			wantObserved: "tank/jails/media is mounted at /srv/media, expected /usr/local/jails/media",
			wantRemedy:   "zfs inherit mountpoint tank/jails/media"},
		{name: "unreadable", datasetsErr: errors.New("exit status 1"), want: CheckUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := readyHost()
			system.datasets = map[string][]Dataset{"tank": tt.datasets}
			system.datasetsErr = tt.datasetsErr
			report, err := newTestService(system).Readiness(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			c := checkByID(t, report, CheckZFSLayout)
			if c.Status != tt.want || c.Observed != tt.wantObserved {
				t.Errorf("zfs_layout = %s %q, want %s %q", c.Status, c.Observed, tt.want, tt.wantObserved)
			}
			if (c.Remedy != "") != (c.Status == CheckFail) || !strings.Contains(c.Remedy, tt.wantRemedy) {
				t.Errorf("remedy = %q, want it to contain %q", c.Remedy, tt.wantRemedy)
			}
		})
	}
}

// rohan has a pool named media, and media is also one of the layout's names.
// Matching name fragments would find "media" present; full paths do not.
func TestLayoutMatchesFullPaths(t *testing.T) {
	system := readyHost()
	system.pools = []string{"media", "zroot"}
	system.datasets = map[string][]Dataset{
		"zroot": {{Name: "zroot", Mountpoint: "/zroot"}, {Name: "zroot/srv", Mountpoint: "/srv"}},
		"media": {{Name: "media", Mountpoint: "/srv/media"}, {Name: "media/Movies", Mountpoint: "/srv/media/Movies"}},
	}
	for _, pool := range []string{"zroot", "media"} {
		layout, err := NewService(system, &fakeLogger{}, pool).Layout(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(layout.Missing) != 4 {
			t.Errorf("%s: missing = %v, want all four", pool, layout.Missing)
		}
	}
}

func TestLayoutJSONHasNoNulls(t *testing.T) {
	layout, err := newTestService(readyHost()).Layout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(layout)
	if strings.Contains(string(b), "null") || !strings.Contains(string(b), `"missing":[]`) {
		t.Errorf("layout = %s, want [] rather than null", b)
	}
}

func TestEnsureLayout(t *testing.T) {
	parentProps := map[string]string{"mountpoint": "/usr/local/jails"}
	tests := []struct {
		name        string
		datasets    []Dataset
		wantCreates []createCall
	}{
		// The acceptance criterion: everything in place runs no zfs create.
		{name: "complete", datasets: fullLayout("tank"), wantCreates: nil},
		{name: "children missing",
			datasets: []Dataset{{Name: "tank", Mountpoint: "none"}, {Name: "tank/jails", Mountpoint: "/usr/local/jails"},
				{Name: "tank/jails/templates", Mountpoint: "/usr/local/jails/templates"}},
			wantCreates: []createCall{{"tank/jails/media", nil}, {"tank/jails/containers", nil}}},
		// Parent first, and only the parent is given a mountpoint.
		{name: "nothing provisioned", datasets: []Dataset{{Name: "tank", Mountpoint: "none"}},
			wantCreates: []createCall{{"tank/jails", parentProps}, {"tank/jails/media", nil},
				{"tank/jails/templates", nil}, {"tank/jails/containers", nil}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := readyHost()
			system.datasets = map[string][]Dataset{"tank": tt.datasets}
			logger := &fakeLogger{}
			change, err := NewService(system, logger, "").EnsureLayout(context.Background())
			if err != nil {
				t.Fatalf("EnsureLayout() error = %v", err)
			}
			if !reflect.DeepEqual(system.creates, tt.wantCreates) {
				t.Errorf("creates = %v, want %v", system.creates, tt.wantCreates)
			}
			wantCreated := make([]string, 0)
			for _, c := range tt.wantCreates {
				wantCreated = append(wantCreated, c.name)
			}
			if change.Changed != (len(wantCreated) > 0) || !slices.Equal(change.Created, wantCreated) {
				t.Errorf("change = %+v, want created %v", change, wantCreated)
			}
			if len(change.Missing) != 0 {
				t.Errorf("missing after ensure = %v", change.Missing)
			}
			// One audit entry per zfs create, and none when nothing ran.
			if len(logger.entries) != len(wantCreated) {
				t.Fatalf("audit entries = %d, want %d", len(logger.entries), len(wantCreated))
			}
			for i, e := range logger.entries {
				if e.Operation != "create-dataset" || e.Target != wantCreated[i] || !e.Success {
					t.Errorf("entry %d = %+v", i, e)
				}
			}

			// A second run changes nothing and runs nothing.
			before := len(system.creates)
			again, err := NewService(system, logger, "").EnsureLayout(context.Background())
			if err != nil || again.Changed || len(again.Created) != 0 || len(system.creates) != before {
				t.Errorf("second run = %+v, %v; ran %d more creates", again, err, len(system.creates)-before)
			}
		})
	}
}

// Concurrent ensures are serialised: both succeed, between them creating each
// missing dataset once. Under -race this also proves the stub is never
// touched by two ensures at once.
func TestEnsureLayoutConcurrently(t *testing.T) {
	stub := readyHost()
	stub.datasets = map[string][]Dataset{"tank": {{Name: "tank", Mountpoint: "none"}}}
	service := newTestService(stub)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := service.EnsureLayout(context.Background())
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("EnsureLayout() error = %v", err)
		}
	}
	if len(stub.creates) != 4 {
		t.Errorf("creates = %v, want each of the four once", stub.creates)
	}
}

func TestEnsureLayoutRefusesMisplacedDataset(t *testing.T) {
	system := readyHost()
	d := []Dataset{{Name: "tank", Mountpoint: "none"}, {Name: "tank/jails", Mountpoint: "/srv/jails"}}
	system.datasets = map[string][]Dataset{"tank": d}
	logger := &fakeLogger{}
	_, err := NewService(system, logger, "").EnsureLayout(context.Background())

	var conflict *LayoutConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("EnsureLayout() error = %v, want a LayoutConflictError", err)
	}
	// Refused before anything was created, even the missing children.
	if len(system.creates) != 0 || len(logger.entries) != 0 {
		t.Errorf("ran %v and logged %d entries; want nothing", system.creates, len(logger.entries))
	}
	for _, want := range []string{"tank/jails", "/srv/jails", "/usr/local/jails"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A missing dataset whose path another dataset already holds, on the same pool
// or another: creating it would mount over that one.
func TestEnsureLayoutRefusesToMountOver(t *testing.T) {
	tests := []struct {
		name       string
		pools      []string
		datasets   map[string][]Dataset
		wantErr    string
		wantRemedy string
	}{
		{name: "parent's path held on the same pool", pools: []string{"tank"},
			datasets: map[string][]Dataset{"tank": {{Name: "tank", Mountpoint: "none"},
				{Name: "tank/old-jails", Mountpoint: "/usr/local/jails"}}},
			wantErr:    "tank/jails would be mounted at /usr/local/jails, which tank/old-jails already holds",
			wantRemedy: "Move tank/old-jails off /usr/local/jails"},
		{name: "child's path held", pools: []string{"tank"},
			datasets: map[string][]Dataset{"tank": {{Name: "tank", Mountpoint: "none"},
				{Name: "tank/jails", Mountpoint: "/usr/local/jails"},
				{Name: "tank/jails/templates", Mountpoint: "/usr/local/jails/templates"},
				{Name: "tank/jails/containers", Mountpoint: "/usr/local/jails/containers"},
				{Name: "tank/archive", Mountpoint: "/usr/local/jails/media"}}},
			wantErr: "tank/jails/media would be mounted at /usr/local/jails/media, which tank/archive already holds"},
		{name: "parent's path held on another pool", pools: []string{"backup", "tank"},
			datasets: map[string][]Dataset{"tank": fullLayout("tank"), "backup": {{Name: "backup", Mountpoint: "none"}}},
			wantErr:  "backup/jails would be mounted at /usr/local/jails, which tank/jails already holds", wantRemedy: "one of: tank"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := readyHost()
			stub.pools, stub.datasets = tt.pools, tt.datasets
			pool := tt.pools[0]
			_, err := NewService(stub, &fakeLogger{}, pool).EnsureLayout(context.Background())
			var conflict *LayoutConflictError
			if !errors.As(err, &conflict) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("EnsureLayout() error = %v, want a conflict: %q", err, tt.wantErr)
			}
			if len(stub.creates) != 0 {
				t.Errorf("created %v before refusing", stub.creates)
			}
			report, err := NewService(stub, &fakeLogger{}, pool).Readiness(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			c := checkByID(t, report, CheckZFSLayout)
			if c.Status != CheckFail || !strings.Contains(c.Remedy, tt.wantRemedy) || c.Remedy == "jaildeck init" {
				t.Errorf("zfs_layout = %s, remedy %q; want fail with %q", c.Status, c.Remedy, tt.wantRemedy)
			}
		})
	}
}

func TestEnsureLayoutStopsAtFailedCreate(t *testing.T) {
	stub := readyHost()
	stub.datasets = map[string][]Dataset{"tank": {{Name: "tank", Mountpoint: "none"}}}
	stub.createErr = map[string]error{"tank/jails/media": &system.CommandError{
		Command: "zfs", Result: system.CommandResult{ExitCode: 1}, Err: errors.New("exit status 1"),
	}}
	logger := &fakeLogger{}
	_, err := NewService(stub, logger, "").EnsureLayout(context.Background())

	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusInternalServerError {
		t.Fatalf("EnsureLayout() error = %v, want a 500", err)
	}
	if !strings.Contains(appErr.Message, "after creating tank/jails") {
		t.Errorf("error %q should say what was created before the failure", appErr.Message)
	}
	if len(stub.creates) != 2 {
		t.Errorf("creates = %v, want it to stop at the failure", stub.creates)
	}
	if len(logger.entries) != 2 || !logger.entries[0].Success || logger.entries[1].Success || logger.entries[1].ExitCode != 1 {
		t.Errorf("entries = %+v, want a success then a failure", logger.entries)
	}
}

func TestLayoutWithoutAPool(t *testing.T) {
	tests := []struct {
		name     string
		pools    []string
		poolsErr error
		wantCode int
	}{
		{"several pools", []string{"backup", "tank", "zroot"}, nil, http.StatusConflict},
		{"no ZFS", nil, noZFS(), http.StatusConflict},
		{"zpool timed out", nil, context.DeadlineExceeded, http.StatusGatewayTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := readyHost()
			system.pools, system.poolsErr = tt.pools, tt.poolsErr
			for _, call := range []func(*Service) error{
				func(s *Service) error { _, err := s.Layout(context.Background()); return err },
				func(s *Service) error { _, err := s.EnsureLayout(context.Background()); return err },
			} {
				var appErr *common.AppError
				if err := call(newTestService(system)); !errors.As(err, &appErr) || appErr.Code != tt.wantCode {
					t.Errorf("error = %v, want %d", err, tt.wantCode)
				}
			}
			if len(system.creates) != 0 {
				t.Errorf("created %v without a pool", system.creates)
			}
		})
	}
}

// The audit filter lowercases ?operation= and then compares exactly, so the
// operation name has to be lowercase to be findable at all.
func TestCreateDatasetEntriesAreFilterable(t *testing.T) {
	system := readyHost()
	system.datasets = map[string][]Dataset{"tank": {{Name: "tank", Mountpoint: "none"}}}
	logger := audit.NewFileLogger(filepath.Join(t.TempDir(), "audit.log"))
	if _, err := NewService(system, logger, "").EnsureLayout(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := logger.Recent(context.Background(), 0, audit.Filter{
		Operation: strings.ToLower("Create-Dataset"),
		Targets:   []string{"tank/jails/templates"},
	})
	if err != nil || len(got) != 1 || got[0].Command != "zfs create tank/jails/templates" {
		t.Errorf("Recent() = %+v, %v; want the one templates entry", got, err)
	}
}

func TestLayoutHandlers(t *testing.T) {
	serve := func(h *HostHandler, method string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler := map[string]http.HandlerFunc{http.MethodGet: h.Layout, http.MethodPut: h.EnsureLayout}[method]
		handler(rec, httptest.NewRequest(method, "/api/host/layout", nil))
		return rec
	}

	system := readyHost()
	system.datasets = map[string][]Dataset{"tank": {{Name: "tank", Mountpoint: "none"}}}
	h := NewHostHandler(newTestService(system))

	if rec := serve(h, http.MethodGet); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"present":false`) {
		t.Errorf("GET = %d %s", rec.Code, rec.Body)
	}
	if len(system.creates) != 0 {
		t.Fatal("GET created datasets")
	}
	if rec := serve(h, http.MethodPut); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"changed":true`) {
		t.Errorf("first PUT = %d %s, want 201", rec.Code, rec.Body)
	}
	if rec := serve(h, http.MethodPut); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"created":[]`) {
		t.Errorf("second PUT = %d %s, want 200 with nothing created", rec.Code, rec.Body)
	}

	system.datasets["tank"][1].Mountpoint = "/srv/jails"
	rec := serve(h, http.MethodPut)
	var body struct {
		Error     string          `json:"error"`
		Conflicts []LayoutDataset `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusConflict {
		t.Fatalf("misplaced PUT = %d %s", rec.Code, rec.Body)
	}
	want := LayoutDataset{Name: "tank/jails", Present: true, Mountpoint: "/srv/jails", ExpectedMountpoint: "/usr/local/jails"}
	if len(body.Conflicts) != 1 || body.Conflicts[0] != want || body.Error == "" {
		t.Errorf("409 body = %+v, want conflict %+v", body, want)
	}
}
