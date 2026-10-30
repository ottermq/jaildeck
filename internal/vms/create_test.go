package vms

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
)

// maker is a vm-bhyve that creates guests: it lists what it has made, and
// any step can be told to fail.
type maker struct {
	listing  Listing
	switches []string
	fail     map[string]error
	calls    []string
	specs    []CreateSpec
	disk     struct {
		snapshot string
		size     int64
	}
	// hostCPUs is hw.ncpu, and applied what each guest it made reads back
	// as: what the host would hold after the same steps.
	hostCPUs int
	applied  map[string]*Applied
}

func newMaker() *maker {
	return &maker{
		listing:  Listing{VMBhyve: VMBhyveReady, VMs: []VM{{Name: "existing", State: StateStopped}}},
		switches: []string{"jd-capsw"},
		fail:     map[string]error{},
		hostCPUs: 16,
		applied:  map[string]*Applied{},
	}
}

func (m *maker) step(name string) error {
	m.calls = append(m.calls, name)
	return m.fail[name]
}

func (m *maker) set(name, state string) {
	for i := range m.listing.VMs {
		if m.listing.VMs[i].Name == name {
			m.listing.VMs[i].State = state
			return
		}
	}
	m.listing.VMs = append(m.listing.VMs, VM{Name: name, State: state})
}

func (m *maker) VMList(context.Context) (Listing, error)            { return m.listing, nil }
func (m *maker) VMInfo(context.Context, string) (Info, error)       { return Info{}, nil }
func (m *maker) VMSwitches(context.Context) ([]string, error)       { return m.switches, m.fail["switches"] }
func (m *maker) VMStop(context.Context, string) (string, error)     { return "", errNotScripted }
func (m *maker) VMPoweroff(context.Context, string) (string, error) { return "", errNotScripted }

func (m *maker) VMStart(_ context.Context, name string) (string, error) {
	if err := m.step("start"); err != nil {
		return "vm start " + name, err
	}
	m.set(name, StateRunning)
	return "vm start " + name, nil
}

func (m *maker) VMCreate(_ context.Context, spec CreateSpec) (string, error) {
	m.specs = append(m.specs, spec)
	if err := m.step("create"); err != nil {
		return "vm create " + spec.Name, err
	}
	m.set(spec.Name, StateStopped)
	m.applied[spec.Name] = &Applied{CPU: strconv.Itoa(spec.CPU), Memory: spec.Memory, Seed: true,
		Address: spec.Address, Gateway: spec.Gateway, Nameservers: spec.Nameservers, SSHKeys: spec.SSHKeys, PasswordHash: spec.PasswordHash}
	return "vm create " + spec.Name, nil
}

func (m *maker) VMAddNetwork(_ context.Context, name, sw string) (string, error) {
	if err := m.step("network"); err != nil {
		return "vm add -d network -s " + sw + " " + name, err
	}
	m.applied[name].Switches = append(m.applied[name].Switches, sw)
	return "vm add -d network -s " + sw + " " + name, nil
}

func (m *maker) VMReplaceDisk(_ context.Context, name, snapshot string, size int64) (string, error) {
	m.disk.snapshot, m.disk.size = snapshot, size
	if err := m.step("disk"); err != nil {
		return "zfs clone " + snapshot, err
	}
	m.applied[name].Origin, m.applied[name].DiskSize = snapshot, size
	return "zfs clone " + snapshot, nil
}

func (m *maker) HostCPUs(context.Context) (int, error) { return m.hostCPUs, m.fail["ncpu"] }

func (m *maker) VMPinCPUs(_ context.Context, name string, cpus []int) (string, error) {
	command := "sysrc -f " + name + ".conf bhyve_options=" + PinOptions(cpus)
	if err := m.step("pin"); err != nil {
		return command, err
	}
	m.applied[name].BhyveOptions = PinOptions(cpus)
	return command, nil
}

func (m *maker) VMApplied(_ context.Context, name string) (Applied, error) {
	if err := m.fail["applied"]; err != nil {
		return Applied{}, err
	}
	if a, ok := m.applied[name]; ok {
		return *a, nil
	}
	return Applied{}, ErrNoSuchVM
}

func (m *maker) VMDestroy(_ context.Context, name string) (string, error) {
	if err := m.step("destroy"); err != nil {
		return "vm destroy -f " + name, err
	}
	m.listing.VMs = slices.DeleteFunc(m.listing.VMs, func(vm VM) bool { return vm.Name == name })
	delete(m.applied, name)
	return "vm destroy -f " + name, nil
}

// catalog has debian-13, ready, at the size of the real one (JD-037).
type catalog map[string]ImageRef

func (c catalog) Image(_ context.Context, name string) (ImageRef, error) {
	if ref, ok := c[name]; ok {
		return ref, nil
	}
	return ImageRef{}, ErrNoSuchImage
}

var debian = catalog{
	"debian-13":    {Ready: true, Snapshot: "tank/vm/.images/debian-13@base", Size: 3221225472},
	"half-written": {Ready: false, Size: 3221225472},
}

func newMakerService(m *maker) (*Service, *auditRecorder) {
	rec := &auditRecorder{}
	s := NewService(m, debian, rec)
	s.wait = func(context.Context, time.Duration) {}
	return s, rec
}

const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFMz6uwq6uCySEimJJLIKB1qaSuK3GpjZ0BSxasBIfDG jd-capture"

func wantAppError(t *testing.T, err error, code int, contains string) {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != code || !strings.Contains(appErr.Message, contains) {
		t.Errorf("err = %v, want %d containing %q", err, code, contains)
	}
}

// --- making one --------------------------------------------------------------

func TestCreateRunsEveryStepInOrderAndAuditsThem(t *testing.T) {
	m := newMaker()
	s, rec := newMakerService(m)
	created, err := s.Create(context.Background(), CreateRequest{
		Name: "jd-cap-made", Image: "debian-13", CPU: 2, Memory: "1G", Disk: "8G", Switch: "jd-capsw",
		Network: &StaticNetwork{Address: "10.77.0.10/24", Gateway: "10.77.0.1", Nameservers: []string{"10.77.0.1"}},
		SSHKeys: []string{"  " + key + "\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "jd-cap-made" || created.Image != "debian-13" || created.State != StateStopped {
		t.Errorf("created = %+v", created)
	}
	if !slices.Equal(m.calls, []string{"create", "network", "disk"}) {
		t.Errorf("calls = %v", m.calls)
	}
	spec := m.specs[0]
	if spec.CPU != 2 || spec.Memory != "1G" || spec.Disk != "8G" || spec.Address != "10.77.0.10/24" ||
		spec.Gateway != "10.77.0.1" || !slices.Equal(spec.Nameservers, []string{"10.77.0.1"}) ||
		!slices.Equal(spec.SSHKeys, []string{key}) {
		t.Errorf("spec = %+v", spec)
	}
	if m.disk.snapshot != "tank/vm/.images/debian-13@base" || m.disk.size != 8<<30 {
		t.Errorf("disk = %+v, want the image's @base at 8G", m.disk)
	}
	want := []string{
		"vm-create vm create jd-cap-made ok",
		"vm-add-network vm add -d network -s jd-capsw jd-cap-made ok",
		"vm-replace-disk zfs clone tank/vm/.images/debian-13@base ok",
	}
	if got := rec.summary(); !slices.Equal(got, want) {
		t.Errorf("audit = %v\nwant %v", got, want)
	}
}

func TestCreateDefaultsAndNoSwitchMeansNoNIC(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	if _, err := s.Create(context.Background(), CreateRequest{Name: "plain", Image: "debian-13"}); err != nil {
		t.Fatal(err)
	}
	spec := m.specs[0]
	if spec.CPU != 1 || spec.Memory != "1G" || spec.Disk != "3221225472" || spec.Address != "" {
		t.Errorf("spec = %+v, want 1 CPU, 1G, the image's size and DHCP", spec)
	}
	if slices.Contains(m.calls, "network") {
		t.Error("added a NIC with no switch named")
	}
	if m.disk.size != 3221225472 {
		t.Errorf("disk size = %d, want the image's", m.disk.size)
	}
}

func TestCreateStartsWhenAsked(t *testing.T) {
	m := newMaker()
	s, rec := newMakerService(m)
	created, err := s.Create(context.Background(), CreateRequest{Name: "web", Image: "debian-13", Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.State != StateRunning || !slices.Equal(m.calls, []string{"create", "disk", "start"}) {
		t.Errorf("created = %+v, calls = %v", created, m.calls)
	}
	if got := rec.summary(); got[len(got)-1] != "vm-start vm start web ok" {
		t.Errorf("audit = %v", got)
	}
}

// A guest that is whole and would not start stays: it is not half made.
func TestAStartThatFailsLeavesTheGuest(t *testing.T) {
	m := newMaker()
	m.fail["start"] = errors.New("exit status 1: guest appears to be running already")
	s, _ := newMakerService(m)
	_, err := s.Create(context.Background(), CreateRequest{Name: "web", Image: "debian-13", Start: true})
	wantAppError(t, err, http.StatusInternalServerError, "could not start")
	if slices.Contains(m.calls, "destroy") {
		t.Error("destroyed a whole guest because it did not start")
	}
}

// --- a step that fails -------------------------------------------------------

func TestAStepThatFailsDestroysTheGuestAgain(t *testing.T) {
	for _, step := range []string{"network", "disk"} {
		t.Run(step, func(t *testing.T) {
			m := newMaker()
			m.fail[step] = errors.New("exit status 1: boom")
			s, rec := newMakerService(m)
			_, err := s.Create(context.Background(), CreateRequest{Name: "web", Image: "debian-13", Switch: "jd-capsw"})
			wantAppError(t, err, http.StatusInternalServerError, `"web" was destroyed again`)
			if m.calls[len(m.calls)-1] != "destroy" {
				t.Errorf("calls = %v, want a destroy last", m.calls)
			}
			if got := rec.summary(); got[len(got)-1] != "vm-destroy vm destroy -f web ok" {
				t.Errorf("audit = %v", got)
			}
			if slices.ContainsFunc(m.listing.VMs, func(vm VM) bool { return vm.Name == "web" }) {
				t.Error("the half-made guest is still listed")
			}
		})
	}
}

func TestADestroyThatFailsTooSaysTheGuestIsStillThere(t *testing.T) {
	m := newMaker()
	m.fail["disk"] = errors.New("exit status 1: dataset is busy")
	m.fail["destroy"] = errors.New("exit status 1: failed to destroy ZFS dataset")
	s, _ := newMakerService(m)
	_, err := s.Create(context.Background(), CreateRequest{Name: "web", Image: "debian-13"})
	wantAppError(t, err, http.StatusInternalServerError, "it is still there, half made")
}

// vm create that fails made nothing Jail Deck can name, so nothing is undone.
func TestAVMCreateThatFailsIsSaid(t *testing.T) {
	m := newMaker()
	m.fail["create"] = errors.New("exit status 1: virtual machine already exists in /vm/web")
	s, rec := newMakerService(m)
	_, err := s.Create(context.Background(), CreateRequest{Name: "web", Image: "debian-13"})
	wantAppError(t, err, http.StatusInternalServerError, "already exists")
	if !slices.Equal(m.calls, []string{"create"}) || len(rec.entries) != 1 || rec.entries[0].Success {
		t.Errorf("calls = %v, audit = %v", m.calls, rec.summary())
	}
}

// --- refused before anything runs --------------------------------------------

func TestCreateRefuses(t *testing.T) {
	cases := []struct {
		name     string
		req      CreateRequest
		setup    func(*maker)
		code     int
		contains string
	}{
		{"a bad name", CreateRequest{Name: "-web", Image: "debian-13"}, nil, 400, "not a vm-bhyve guest name"},
		{"too many CPUs", CreateRequest{Name: "web", Image: "debian-13", CPU: 65}, nil, 400, "from 1 to 64"},
		{"negative CPUs", CreateRequest{Name: "web", Image: "debian-13", CPU: -1}, nil, 400, "from 1 to 64"},
		{"memory not vm-bhyve's", CreateRequest{Name: "web", Image: "debian-13", Memory: "1.5G"}, nil, 400, "512M, 2G"},
		{"memory without a unit", CreateRequest{Name: "web", Image: "debian-13", Memory: "512"}, nil, 400, "512M, 2G"},
		{"a disk unit zfs would not take", CreateRequest{Name: "web", Image: "debian-13", Disk: "20GB"}, nil, 400, "M, G or T"},
		{"a disk smaller than the image", CreateRequest{Name: "web", Image: "debian-13", Disk: "2G"}, nil, 400, "the image's is 3G"},
		{"not a key", CreateRequest{Name: "web", Image: "debian-13", SSHKeys: []string{"hunter2"}}, nil, 400, "ssh_keys[0]"},
		{"two keys in one", CreateRequest{Name: "web", Image: "debian-13", SSHKeys: []string{key + "\n" + key}}, nil, 400, "ssh_keys[0]"},
		// The comment would make vm-bhyve's unquoted YAML list item a mapping.
		{"a comment YAML misreads", CreateRequest{Name: "web", Image: "debian-13", SSHKeys: []string{key + ": x"}}, nil, 400, "ssh_keys[0]"},
		{"an address with no switch", CreateRequest{Name: "web", Image: "debian-13",
			Network: &StaticNetwork{Address: "10.0.0.5/24"}}, nil, 400, "switch is not"},
		{"an address with no prefix", CreateRequest{Name: "web", Image: "debian-13", Switch: "jd-capsw",
			Network: &StaticNetwork{Address: "10.0.0.5"}}, nil, 400, "with its prefix"},
		{"a netconfig separator", CreateRequest{Name: "web", Image: "debian-13", Switch: "jd-capsw",
			Network: &StaticNetwork{Address: "10.0.0.5/24;mtu=9000"}}, nil, 400, "with its prefix"},
		{"a gateway of the other version", CreateRequest{Name: "web", Image: "debian-13", Switch: "jd-capsw",
			Network: &StaticNetwork{Address: "10.0.0.5/24", Gateway: "fe80::1"}}, nil, 400, "same IP version"},
		{"a nameserver that is not an address", CreateRequest{Name: "web", Image: "debian-13", Switch: "jd-capsw",
			Network: &StaticNetwork{Address: "10.0.0.5/24", Nameservers: []string{"dns.example"}}}, nil, 400, "nameservers[0]"},
		{"an image nobody prepared", CreateRequest{Name: "web", Image: "ubuntu"}, nil, 400, "prepare one first"},
		{"an image not ready", CreateRequest{Name: "web", Image: "half-written"}, nil, 409, "is not ready"},
		{"a name taken", CreateRequest{Name: "existing", Image: "debian-13"}, nil, 409, "already has a guest"},
		{"a switch vm-bhyve does not have", CreateRequest{Name: "web", Image: "debian-13", Switch: "public"}, nil,
			400, "it has jd-capsw"},
		{"no switches at all", CreateRequest{Name: "web", Image: "debian-13", Switch: "public"},
			func(m *maker) { m.switches = nil }, 400, "it has none"},
		{"vm-bhyve not configured", CreateRequest{Name: "web", Image: "debian-13"},
			func(m *maker) {
				m.listing = Listing{VMBhyve: VMBhyveNotConfigured, Problem: "$vm_enable is not enabled in /etc/rc.conf!"}
			}, 409, "$vm_enable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMaker()
			if tc.setup != nil {
				tc.setup(m)
			}
			s, rec := newMakerService(m)
			_, err := s.Create(context.Background(), tc.req)
			wantAppError(t, err, tc.code, tc.contains)
			if len(m.calls) != 0 || len(rec.entries) != 0 {
				t.Errorf("ran %v, audited %v", m.calls, rec.summary())
			}
		})
	}
}

func TestCreateWithoutImagesIsNotImplemented(t *testing.T) {
	s := NewService(newMaker(), nil, nil)
	_, err := s.Create(context.Background(), CreateRequest{Name: "web", Image: "debian-13"})
	wantAppError(t, err, http.StatusNotImplemented, "no VM images")
}

func TestBytesOf(t *testing.T) {
	for in, want := range map[string]int64{"512M": 512 << 20, "8G": 8 << 30, "1T": 1 << 40, "3221225472": 3221225472} {
		if got, ok := bytesOf(in); !ok || got != want {
			t.Errorf("bytesOf(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "0", "0G", "8g", "8GB", "-1", "1.5G"} {
		if _, ok := bytesOf(in); ok {
			t.Errorf("bytesOf(%q) accepted", in)
		}
	}
}

// --- destroying --------------------------------------------------------------

func TestDestroy(t *testing.T) {
	cases := []struct {
		state, confirm string
		code           int
		contains       string
	}{
		{StateStopped, "", 400, "confirm=existing"},
		{StateRunning, "existing", 409, "stop it first"},
		{StateBootloader, "existing", 409, "stop it first"},
		{StateLocked, "existing", 409, "is locked by"},
	}
	for _, tc := range cases {
		t.Run(tc.state+" "+tc.confirm, func(t *testing.T) {
			m := newMaker()
			m.set("existing", tc.state)
			s, _ := newMakerService(m)
			_, err := s.Destroy(context.Background(), "existing", tc.confirm)
			wantAppError(t, err, tc.code, tc.contains)
			if len(m.calls) != 0 {
				t.Errorf("ran %v", m.calls)
			}
		})
	}

	// This host's own lock, left by a vm-run that will not exit: waited out,
	// then refused in its own words, and nothing run.
	own := newMaker()
	own.listing.VMs[0].State, own.listing.VMs[0].LockedBy = StateLocked, "isengard"
	ownService, _ := newMakerService(own)
	ownService.hostname = "isengard"
	var waited time.Duration
	ownService.wait = func(_ context.Context, d time.Duration) { waited += d }
	_, err := ownService.Destroy(context.Background(), "existing", "existing")
	wantAppError(t, err, http.StatusConflict, "still locked by this host")
	if waited != OwnLockWait {
		t.Errorf("waited %s for this host's own lock, want %s", waited, OwnLockWait)
	}
	if len(own.calls) != 0 {
		t.Errorf("ran %v", own.calls)
	}

	m := newMaker()
	s, rec := newMakerService(m)
	removal, err := s.Destroy(context.Background(), "existing", "existing")
	if err != nil || removal.Name != "existing" {
		t.Fatalf("Destroy = %+v, %v", removal, err)
	}
	if got := rec.summary(); !slices.Equal(got, []string{"vm-destroy vm destroy -f existing ok"}) {
		t.Errorf("audit = %v", got)
	}
	_, err = s.Destroy(context.Background(), "existing", "existing")
	wantAppError(t, err, http.StatusNotFound, "no virtual machine")
}

func (m *maker) VMDatasets(context.Context) (map[string]string, error) {
	return map[string]string{"default": "tank/vm"}, nil
}
