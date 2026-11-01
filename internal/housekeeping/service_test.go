package housekeeping

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
)

type stubTemplates struct {
	list []Template
	err  error
}

func (s stubTemplates) Templates(context.Context) ([]Template, error) { return s.list, s.err }

type stubImages struct{ list []Image }

func (s stubImages) Images(context.Context) ([]Image, error) { return s.list, nil }

type stubRetained struct{ list []Retained }

func (s stubRetained) Retained(context.Context) ([]Retained, error) { return s.list, nil }

type stubSnapshots struct {
	list []Snapshot
	errs []error
}

func (s stubSnapshots) JailSnapshots(context.Context) ([]Snapshot, []error) { return s.list, s.errs }

type stubArchives struct{ list []Archive }

func (s stubArchives) Archives(context.Context) ([]Archive, error) { return s.list, nil }

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// A host shaped like isengard's: two templates in use and one not, an image
// no guest uses, a retained dataset, a jail's own snapshots old and new, and
// zrepl's beside them.
func isengardLike() Sources {
	return Sources{
		Templates: stubTemplates{list: []Template{
			{Name: "15.1-RELEASE", Used: 430 << 20, Ready: true, Dependents: 2},
			{Name: "pg18", Used: 900 << 20, Ready: true, Dependents: 2},
			{Name: "14.3-RELEASE", Used: 380 << 20, Ready: true},
			{Name: "half", Used: 10 << 20},
			{Name: "preparing", Used: 5 << 20, Busy: true},
		}},
		Images: stubImages{list: []Image{
			{Name: "debian-13", Used: 541 << 20, Ready: true},
			{Name: "used", Used: 100, Ready: true, Guests: 1},
		}},
		Retained: stubRetained{list: []Retained{{Name: "kept", Dataset: "tank/jails/retained/kept", Used: 2 << 20}}},
		Snapshots: stubSnapshots{list: []Snapshot{
			{Jail: "postgres", Name: "tank/jails/containers/postgres@clean-installed", Short: "clean-installed",
				Used: 920 << 10, Created: now.AddDate(0, -2, 0)},
			{Jail: "postgres", Name: "tank/jails/containers/postgres@yesterday", Short: "yesterday",
				Used: 1, Created: now.AddDate(0, 0, -1)},
			{Jail: "postgres", Name: "tank/jails/containers/postgres@zrepl_20260822_224538_000", Short: "zrepl_20260822_224538_000",
				Used: 3 << 20, Created: now.AddDate(0, -2, 0)},
			{Jail: "web", Name: "tank/jails/containers/web@autosnap_2026-08-01_daily", Short: "autosnap_2026-08-01_daily",
				Created: now.AddDate(0, -3, 0)},
		}},
		Archives: stubArchives{list: []Archive{{Path: "/usr/local/jails/media/15.1-RELEASE/base.txz",
			Size: 164624792, Modified: now.AddDate(0, -4, 0), For: "15.1-RELEASE"}}},
	}
}

func service(sources Sources) *Service {
	s := NewService(sources)
	s.now = func() time.Time { return now }
	return s
}

func TestWhatNothingUsesIsListedWithItsRemoval(t *testing.T) {
	report, err := service(isengardLike()).Reclaimable(context.Background(), DefaultSnapshotAge)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Item{}
	for _, item := range report.Items {
		got[item.Kind+" "+item.Name] = item
	}
	want := map[string]string{
		"template 14.3-RELEASE": "/api/templates/14.3-RELEASE?confirm=14.3-RELEASE",
		"template half":         "/api/templates/half?confirm=half",
		"image debian-13":       "/api/vm-images/debian-13?confirm=debian-13",
		"retained kept":         "/api/retained/kept?confirm=kept",
		"snapshot tank/jails/containers/postgres@clean-installed": "/api/jails/postgres/snapshots/clean-installed" +
			"?confirm=tank%2Fjails%2Fcontainers%2Fpostgres%40clean-installed",
		"archive /usr/local/jails/media/15.1-RELEASE/base.txz": "",
	}
	if len(got) != len(want) {
		t.Errorf("items = %v", keys(got))
	}
	for key, path := range want {
		item, ok := got[key]
		if !ok {
			t.Errorf("%s is not listed", key)
			continue
		}
		if path == "" {
			if item.Remove != nil {
				t.Errorf("%s has a removal: %+v", key, item.Remove)
			}
			continue
		}
		if item.Remove == nil || item.Remove.Method != http.MethodDelete || item.Remove.Path != path {
			t.Errorf("%s: remove = %+v, want DELETE %s", key, item.Remove, path)
		}
	}
	if got["template half"].Why == got["template 14.3-RELEASE"].Why {
		t.Error("a template that is not ready reads as an ordinary unused one")
	}
	var total int64
	for _, item := range report.Items {
		total += item.Size
	}
	if report.Total != total || report.Kinds[KindTemplate] != (380+10)<<20 {
		t.Errorf("total %d (items %d), kinds %v", report.Total, total, report.Kinds)
	}
	if report.SkippedSnapshots["zrepl_"] != 1 || report.SkippedSnapshots["autosnap_"] != 1 {
		t.Errorf("skipped = %v", report.SkippedSnapshots)
	}
	if len(report.Problems) != 0 || report.SnapshotAge != 30 {
		t.Errorf("problems %v, age %d", report.Problems, report.SnapshotAge)
	}
	// Ordered by kind, the largest first within one.
	if report.Items[0].Name != "14.3-RELEASE" || report.Items[len(report.Items)-1].Kind != KindArchive {
		t.Errorf("order = %v", keys(got))
	}
}

func TestTheSnapshotAgeIsTheClients(t *testing.T) {
	report, err := service(isengardLike()).Reclaimable(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := 0
	for _, item := range report.Items {
		if item.Kind == KindSnapshot {
			snapshots++
		}
	}
	if snapshots != 1 {
		// Exactly a day old is not older than a day.
		t.Errorf("snapshots = %d", snapshots)
	}
	for _, age := range []int{0, -1, 3651} {
		_, err := service(isengardLike()).Reclaimable(context.Background(), age)
		var appErr *common.AppError
		if !errors.As(err, &appErr) || appErr.Code != http.StatusBadRequest {
			t.Errorf("age %d: %v", age, err)
		}
	}
}

func TestASourceThatCannotBeReadDoesNotHideTheOthers(t *testing.T) {
	sources := isengardLike()
	sources.Templates = stubTemplates{err: errors.New("zfs list: exit 1")}
	sources.Snapshots = stubSnapshots{errs: []error{errors.New("web: dataset is busy")}}
	report, err := service(sources).Reclaimable(context.Background(), DefaultSnapshotAge)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 2 || report.Problems[0] != "templates: zfs list: exit 1" {
		t.Errorf("problems = %v", report.Problems)
	}
	if report.Kinds[KindImage] == 0 || report.Kinds[KindArchive] == 0 {
		t.Errorf("kinds = %v", report.Kinds)
	}
}

func TestAHostWithNothingHasAnEmptyReport(t *testing.T) {
	report, err := service(Sources{}).Reclaimable(context.Background(), DefaultSnapshotAge)
	if err != nil || report.Items == nil || len(report.Items) != 0 || report.Total != 0 || report.Problems == nil {
		t.Errorf("report = %+v, %v", report, err)
	}
}

func keys(m map[string]Item) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
