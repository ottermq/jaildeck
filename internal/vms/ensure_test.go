package vms

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
)

// --- pinning (JD-041) ----------------------------------------------------------

func TestCreatePinsBeforeTheNetworkAndAuditsIt(t *testing.T) {
	m := newMaker()
	s, rec := newMakerService(m)
	_, err := s.Create(context.Background(), CreateRequest{
		Name: "pinned", Image: "debian-13", CPU: 2, CPUSet: []int{2, 3}, Switch: "jd-capsw"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.calls, []string{"create", "pin", "network", "disk"}) {
		t.Errorf("calls = %v", m.calls)
	}
	if got := m.applied["pinned"].BhyveOptions; got != "-p 0:2 -p 1:3" {
		t.Errorf("bhyve_options = %q", got)
	}
	if !slices.Contains(rec.operations(), OperationPinCPUs) {
		t.Errorf("audited %v, want %s", rec.operations(), OperationPinCPUs)
	}
}

func TestAnUnpinnedGuestWritesNoOptions(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	if _, err := s.Create(context.Background(), CreateRequest{Name: "free", Image: "debian-13", CPU: 2}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(m.calls, "pin") {
		t.Errorf("calls = %v; a guest with no cpuset is not pinned", m.calls)
	}
}

func TestCPUSetsRefusedBeforeAnythingRuns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cpu    int
		cpuset []int
		want   string
	}{
		{"fewer than the vCPUs", 2, []int{2}, "names 1 host CPUs for 2 vCPUs"},
		{"more than the vCPUs", 1, []int{2, 3}, "names 2 host CPUs for 1 vCPUs"},
		{"the default cpu is one", 0, []int{2, 3}, "for 1 vCPUs"},
		{"twice the same", 2, []int{3, 3}, "host CPU 3 twice"},
		{"negative", 1, []int{-1}, "numbered from 0"},
		// Measured: bhyve aborts on it at start, and vm start exits 0.
		{"past the host's", 2, []int{2, 16}, "cpuset[1] is 16 and this host has CPUs 0 to 15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMaker()
			s, _ := newMakerService(m)
			_, err := s.Create(context.Background(), CreateRequest{
				Name: "pinned", Image: "debian-13", CPU: tc.cpu, CPUSet: tc.cpuset})
			wantAppError(t, err, http.StatusBadRequest, tc.want)
			if len(m.calls) != 0 {
				t.Errorf("ran %v", m.calls)
			}
		})
	}
}

func TestAPinThatFailsDestroysTheGuest(t *testing.T) {
	m := newMaker()
	m.fail["pin"] = errors.New("sysrc: permission denied")
	s, _ := newMakerService(m)
	_, err := s.Create(context.Background(), CreateRequest{Name: "pinned", Image: "debian-13", CPU: 1, CPUSet: []int{4}})
	wantAppError(t, err, http.StatusInternalServerError, "could not pin its vCPUs")
	if !slices.Equal(m.calls, []string{"create", "pin", "destroy"}) {
		t.Errorf("calls = %v", m.calls)
	}
}

func TestAHostThatCannotSayItsCPUsIsNotGuessedAt(t *testing.T) {
	m := newMaker()
	m.fail["ncpu"] = errors.New("sysctl: unknown oid")
	s, _ := newMakerService(m)
	_, err := s.Create(context.Background(), CreateRequest{Name: "pinned", Image: "debian-13", CPU: 1, CPUSet: []int{0}})
	wantAppError(t, err, http.StatusInternalServerError, "how many CPUs")
	if len(m.calls) != 0 {
		t.Errorf("ran %v", m.calls)
	}
}

func TestParsePinsReadsOnlyWhatPinOptionsWrites(t *testing.T) {
	for options, want := range map[string][]int{
		"-p 0:2 -p 1:3":      {2, 3},
		"-p 0:7":             {7},
		"  -p 0:2   -p 1:3 ": {2, 3},
	} {
		if got, ok := parsePins(options); !ok || !slices.Equal(got, want) {
			t.Errorf("parsePins(%q) = %v, %v; want %v", options, got, ok, want)
		}
	}
	for _, options := range []string{
		"", "-p 1:3", "-p 0:2 -p 2:3", "-p 1:3 -p 0:2", "-p 0:2 -S", "-S", "-p 0:x", "-p 0:-1", "-p 0:02", "-p 0", "-P 0:2",
	} {
		if got, ok := parsePins(options); ok {
			t.Errorf("parsePins(%q) = %v; want not a cpuset", options, got)
		}
	}
	if got := PinOptions([]int{5, 9, 1}); got != "-p 0:5 -p 1:9 -p 2:1" {
		t.Errorf("PinOptions = %q", got)
	}
}

func TestDetailCarriesThePin(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	if _, err := s.Create(context.Background(), CreateRequest{Name: "pinned", Image: "debian-13", CPU: 2, CPUSet: []int{2, 3}}); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Get(context.Background(), "pinned")
	if err != nil || !slices.Equal(detail.CPUSet, []int{2, 3}) {
		t.Errorf("cpuset = %v, %v", detail.CPUSet, err)
	}
	// A configuration that cannot be read costs the pin, not the page.
	m.fail["applied"] = errors.New("permission denied")
	detail, err = s.Get(context.Background(), "pinned")
	if err != nil || detail.CPUSet != nil {
		t.Errorf("unreadable: cpuset = %v, %v; want none and no error", detail.CPUSet, err)
	}
}

// --- PUT (JD-016, JD-041) ------------------------------------------------------

var made = CreateRequest{
	Image: "debian-13", CPU: 2, CPUSet: []int{2, 3}, Memory: "2G", Disk: "8G", Switch: "jd-capsw",
	Network: &StaticNetwork{Address: "10.77.0.10/24", Gateway: "10.77.0.1", Nameservers: []string{"10.77.0.1"}},
	SSHKeys: []string{key}, Start: true,
}

func TestEnsureCreatesAGuestThatIsNotThere(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	ensured, err := s.Ensure(context.Background(), "web", made)
	if err != nil {
		t.Fatal(err)
	}
	if !ensured.Changed || ensured.Name != "web" || ensured.State != StateRunning || ensured.Differences != nil {
		t.Errorf("ensured = %+v", ensured)
	}
}

func TestEnsureTwiceChangesNothingTheSecondTime(t *testing.T) {
	m := newMaker()
	s, rec := newMakerService(m)
	if _, err := s.Ensure(context.Background(), "web", made); err != nil {
		t.Fatal(err)
	}
	calls, audited := len(m.calls), len(rec.operations())
	again, err := s.Ensure(context.Background(), "web", made)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed || again.Differences != nil || again.Name != "web" || again.Image != "debian-13" {
		t.Errorf("again = %+v", again)
	}
	if len(m.calls) != calls || len(rec.operations()) != audited {
		t.Errorf("the second PUT ran %v and audited %v", m.calls[calls:], rec.operations()[audited:])
	}
}

// The defaults resolve the same way on both sides: a PUT that leaves cpu,
// memory and disk out matches the guest those defaults made.
func TestEnsureComparesDefaultsResolved(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	plain := CreateRequest{Image: "debian-13"}
	if _, err := s.Ensure(context.Background(), "plain", plain); err != nil {
		t.Fatal(err)
	}
	again, err := s.Ensure(context.Background(), "plain", plain)
	if err != nil || again.Changed || again.Differences != nil {
		t.Errorf("again = %+v, %v", again, err)
	}
}

func TestEnsureReportsEveryDifferenceAndChangesNothing(t *testing.T) {
	m := newMaker()
	s, rec := newMakerService(m)
	if _, err := s.Ensure(context.Background(), "web", made); err != nil {
		t.Fatal(err)
	}
	calls, audited := len(m.calls), len(rec.operations())
	other := made
	other.CPU, other.CPUSet, other.Memory, other.Disk = 4, nil, "4G", "16G"
	other.Switch = "jd-capsw"
	other.Network = &StaticNetwork{Address: "10.77.0.11/24", Gateway: "10.77.0.254"}
	other.SSHKeys = nil
	ensured, err := s.Ensure(context.Background(), "web", other)
	if err != nil {
		t.Fatal(err)
	}
	want := []Difference{
		{Field: "cpu", Want: "4", Have: "2"},
		{Field: "cpuset", Want: "", Have: "2,3"},
		{Field: "memory", Want: "4G", Have: "2G"},
		{Field: "disk", Want: "16G", Have: "8G"},
		{Field: "network.address", Want: "10.77.0.11/24", Have: "10.77.0.10/24"},
		{Field: "network.gateway", Want: "10.77.0.254", Have: "10.77.0.1"},
		{Field: "network.nameservers", Want: "", Have: "10.77.0.1"},
		{Field: "ssh_keys", Want: "", Have: key},
	}
	if ensured.Changed || !slices.Equal(ensured.Differences, want) {
		t.Errorf("differences = %+v\nwant %+v", ensured.Differences, want)
	}
	if len(m.calls) != calls || len(rec.operations()) != audited {
		t.Errorf("a PUT that differs ran %v", m.calls[calls:])
	}
}

func TestEnsureNamesTheImageAndTheSwitch(t *testing.T) {
	m := newMaker()
	m.switches = []string{"jd-capsw", "jaildeck"}
	s, _ := newMakerService(m)
	if _, err := s.Ensure(context.Background(), "web", made); err != nil {
		t.Fatal(err)
	}
	m.applied["web"].Origin = "tank/vm/.images/debian-12@base"
	other := made
	other.Switch = "jaildeck"
	ensured, err := s.Ensure(context.Background(), "web", other)
	if err != nil {
		t.Fatal(err)
	}
	want := []Difference{
		{Field: "image", Want: "debian-13", Have: "tank/vm/.images/debian-12@base"},
		{Field: "switch", Want: "jaildeck", Have: "jd-capsw"},
	}
	if !slices.Equal(ensured.Differences, want) {
		t.Errorf("differences = %+v", ensured.Differences)
	}
}

func TestEnsureDoesNotCompareStart(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	if _, err := s.Ensure(context.Background(), "web", made); err != nil {
		t.Fatal(err)
	}
	m.set("web", StateStopped)
	calls := len(m.calls)
	ensured, err := s.Ensure(context.Background(), "web", made)
	if err != nil || ensured.Differences != nil || ensured.State != StateStopped || len(m.calls) != calls {
		t.Errorf("ensured = %+v, %v; want a stopped guest left stopped and no differences", ensured, err)
	}
}

func TestEnsureSaysBhyveOptionsItDidNotWrite(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	if _, err := s.Ensure(context.Background(), "web", made); err != nil {
		t.Fatal(err)
	}
	m.applied["web"].BhyveOptions = "-p 0:2 -p 1:3 -S"
	ensured, _ := s.Ensure(context.Background(), "web", made)
	want := []Difference{{Field: "cpuset", Want: "2,3", Have: `bhyve_options="-p 0:2 -p 1:3 -S"`}}
	if !slices.Equal(ensured.Differences, want) {
		t.Errorf("differences = %+v", ensured.Differences)
	}
}

func TestEnsureRefusals(t *testing.T) {
	m := newMaker()
	m.listing.VMs = append(m.listing.VMs, VM{Name: "handmade", State: StateStopped})
	m.applied["handmade"] = &Applied{CPU: "1", Memory: "512M"}
	s, _ := newMakerService(m)
	ctx := context.Background()

	_, err := s.Ensure(ctx, "web", CreateRequest{Name: "api", Image: "debian-13"})
	wantAppError(t, err, http.StatusBadRequest, "a guest has one name")

	// "existing" has no configuration in the default datastore.
	_, err = s.Ensure(ctx, "existing", CreateRequest{Image: "debian-13"})
	wantAppError(t, err, http.StatusConflict, "not in vm-bhyve's default datastore")

	_, err = s.Ensure(ctx, "handmade", CreateRequest{Image: "debian-13"})
	wantAppError(t, err, http.StatusConflict, "no cloud-init seed")

	// A request a create would refuse is refused, not compared.
	_, err = s.Ensure(ctx, "handmade", CreateRequest{Image: "debian-13", CPU: 2, CPUSet: []int{1}})
	wantAppError(t, err, http.StatusBadRequest, "for 2 vCPUs")

	m.fail["applied"] = errors.New("permission denied")
	_, err = s.Ensure(ctx, "handmade", CreateRequest{Image: "debian-13"})
	wantAppError(t, err, http.StatusInternalServerError, "permission denied")
	if len(m.calls) != 0 {
		t.Errorf("ran %v", m.calls)
	}
}

func TestEnsureFailingToCreateSaysWhy(t *testing.T) {
	m := newMaker()
	m.fail["disk"] = errors.New("out of space")
	s, _ := newMakerService(m)
	ensured, err := s.Ensure(context.Background(), "web", made)
	wantAppError(t, err, http.StatusInternalServerError, "destroyed again")
	if ensured.Changed {
		t.Errorf("ensured = %+v; a create undone changed nothing", ensured)
	}
}

func (a *auditRecorder) operations() []string {
	out := make([]string, 0, len(a.entries))
	for _, e := range a.entries {
		out = append(out, e.Operation)
	}
	return out
}
