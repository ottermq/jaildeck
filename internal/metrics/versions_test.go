package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/osversion"
)

// someVersions is a host updated and not yet restarted, with a jail of every
// kind: current, behind, on another release, and unreadable.
func someVersions() OSVersions {
	host := osversion.NewHost("15.1-RELEASE-p5", "15.1-RELEASE-p6", "15.1-RELEASE-p6")
	judge := func(name, userland string) Userland {
		return Userland{Name: name, Standing: osversion.Compare(host.Userland, userland)}
	}
	return OSVersions{
		Host: host,
		Jails: []Userland{
			judge("nginx", "15.1-RELEASE-p6"),
			judge("postgres", "15.1-RELEASE-p2"),
			judge("legacy", "14.3-RELEASE-p4"),
			judge("broken", ""),
		},
		Templates: []Userland{judge("15.1-RELEASE", "15.1-RELEASE-p5")},
	}
}

func TestVersionMetrics(t *testing.T) {
	report := aReport()
	versions := someVersions()
	report.OS = &versions
	body := render(t, report)
	checkFormat(t, body)

	requireLine(t, body, "jaildeck_host_reboot_pending 1")
	requireLine(t, body, `jaildeck_os_version_info{scope="host",name="kernel_running",version="15.1-RELEASE-p5",status=""} 1`)
	requireLine(t, body, `jaildeck_os_version_info{scope="host",name="kernel_installed",version="15.1-RELEASE-p6",status=""} 1`)
	requireLine(t, body, `jaildeck_os_version_info{scope="jail",name="postgres",version="15.1-RELEASE-p2",status="behind"} 1`)
	requireLine(t, body, `jaildeck_os_version_info{scope="jail",name="broken",version="",status="unknown"} 1`)
	requireLine(t, body, `jaildeck_os_version_info{scope="template",name="15.1-RELEASE",version="15.1-RELEASE-p5",status="behind"} 1`)

	requireLine(t, body, `jaildeck_jail_userland_behind{jail="nginx"} 0`)
	requireLine(t, body, `jaildeck_jail_userland_behind{jail="postgres"} 4`)
	requireLine(t, body, `jaildeck_template_userland_behind{template="15.1-RELEASE"} 1`)
	// Another release and an unreadable version are not "0 behind": they
	// are left out, so an alert never fires or clears on a guess.
	for _, absent := range []string{`jail="legacy"`, `jail="broken"`} {
		if strings.Contains(body, "jaildeck_jail_userland_behind{"+absent) {
			t.Errorf("jaildeck_jail_userland_behind written for %s", absent)
		}
	}
}

func TestRebootPendingNeedsBothKernels(t *testing.T) {
	report := aReport()
	report.OS = &OSVersions{Host: osversion.NewHost("15.1-RELEASE-p6", "", "15.1-RELEASE-p6")}
	body := render(t, report)
	checkFormat(t, body)
	if strings.Contains(body, "jaildeck_host_reboot_pending") {
		t.Error("jaildeck_host_reboot_pending written without the installed kernel")
	}
	// No jails or templates: no block, not a HELP with nothing under it.
	if strings.Contains(body, "jaildeck_jail_userland_behind") {
		t.Error("jaildeck_jail_userland_behind written for a host with no jails")
	}
}

func TestNoVersionsNoVersionMetrics(t *testing.T) {
	if body := render(t, aReport()); strings.Contains(body, "jaildeck_os_version_info") {
		t.Error("version metrics written for a report without versions")
	}
}

type countingVersions struct {
	calls int
	fail  error
}

func (c *countingVersions) Versions(context.Context) (OSVersions, error) {
	c.calls++
	if c.fail != nil {
		return OSVersions{}, c.fail
	}
	v := someVersions()
	return v, nil
}

func TestReportCarriesVersionsAndReadsThemOnceAMinute(t *testing.T) {
	source := &countingVersions{}
	service := NewService(aHost(), nil, nil, nil)
	service.SetVersions(source)

	for range 3 {
		report, err := service.Report(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if report.OS == nil || !report.OS.Host.RebootPending {
			t.Fatalf("report.OS = %+v; want the versions", report.OS)
		}
		// Sorted by name, whatever order the source gave.
		if got := report.OS.Jails[0].Name; got != "broken" {
			t.Errorf("first jail = %q; want the list sorted", got)
		}
	}
	if source.calls != 1 {
		t.Errorf("versions read %d times in three reports; want once", source.calls)
	}

	// Once the minute is up, read again.
	service.mu.Lock()
	service.osAt = time.Now().Add(-2 * versionsFor)
	service.mu.Unlock()
	if _, err := service.Report(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.calls != 2 {
		t.Errorf("versions read %d times; want a second read after the minute", source.calls)
	}
}

func TestVersionsThatFailDoNotFailTheReport(t *testing.T) {
	source := &countingVersions{fail: errors.New("sysctl: permission denied")}
	service := NewService(aHost(), nil, nil, nil)
	service.SetVersions(source)

	report, err := service.Report(context.Background())
	if err != nil {
		t.Fatalf("Report = %v; versions must not fail it", err)
	}
	if report.OS != nil || !strings.Contains(report.OSUnavailable, "permission denied") {
		t.Errorf("OS = %+v, OSUnavailable = %q; want none and the reason", report.OS, report.OSUnavailable)
	}
	// A failure is not cached: the next report tries again.
	if _, err := service.Report(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.calls != 2 {
		t.Errorf("versions read %d times; want a retry after a failure", source.calls)
	}
}
