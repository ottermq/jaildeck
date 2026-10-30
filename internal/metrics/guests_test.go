package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubGuests struct {
	list []GuestUsage
	err  error
}

func (s stubGuests) GuestUsage(context.Context) ([]GuestUsage, error) { return s.list, s.err }

// #126: the running guests are in the report, sorted, whatever the jails do.
func TestReportCarriesTheRunningGuests(t *testing.T) {
	service := NewService(aHost(), nil, nil, nil)
	service.SetGuests(stubGuests{list: []GuestUsage{
		{Guest: "web", PCPU: 195.5, MemoryResident: 1019424768},
		{Guest: "db", PCPU: 0.4, MemoryResident: 410562560},
	}})
	report, err := service.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Guests) != 2 || report.Guests[0].Guest != "db" || report.GuestsUnavailable != "" {
		t.Errorf("guests = %+v (%q)", report.Guests, report.GuestsUnavailable)
	}
}

func TestGuestsThatCannotBeReadAreSaidNotRaised(t *testing.T) {
	service := NewService(aHost(), accounting(), aFleet(), nil)
	service.SetGuests(stubGuests{err: errors.New("vm list: exit 1")})
	report, err := service.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Guests == nil || len(report.Guests) != 0 || report.GuestsUnavailable != "vm list: exit 1" || len(report.Jail) == 0 {
		t.Errorf("report = %+v", report)
	}
}

func TestWithoutAGuestSourceGuestsIsEmptyNotNull(t *testing.T) {
	report, err := NewService(aHost(), nil, nil, nil).Report(context.Background())
	if err != nil || report.Guests == nil {
		t.Errorf("guests = %v, %v", report.Guests, err)
	}
}

func TestGuestMetricsAreInCoresAndBytes(t *testing.T) {
	report := aReport()
	report.Guests = []GuestUsage{
		{Guest: "web", PCPU: 195.5, MemoryResident: 1019424768},
		{Guest: "gone", Error: "its bhyve process ended while it was being read"},
	}
	body := render(t, report)
	requireLine(t, body, "jaildeck_guests_running 2")
	requireLine(t, body, `jaildeck_guest_readable{guest="web"} 1`)
	requireLine(t, body, `jaildeck_guest_readable{guest="gone"} 0`)
	requireLine(t, body, `jaildeck_guest_cpu_cores{guest="web"} 1.955`)
	requireLine(t, body, `jaildeck_guest_memory_bytes{guest="web"} 1.019424768e+09`)
	if strings.Contains(body, `jaildeck_guest_cpu_cores{guest="gone"}`) {
		t.Error("an unreadable guest is published as using nothing")
	}
}
