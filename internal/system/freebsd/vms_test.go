package freebsd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vms"
)

// Every byte here is from capture-vm-guests-induced.sh on isengard, which made
// two guests -- one started on a switch of its own, one stopped with no
// network -- captured how vm-bhyve reports them, and destroyed them (see
// testdata/hostfacts/README.md).

const vmSet = "isengard-induced"

func vmAdapter(t *testing.T, captures map[string]string) *Adapter {
	t.Helper()
	return NewAdapter(&replayRunner{t: t, set: vmSet, captures: captures})
}

func vmList(t *testing.T, capture string) vms.Listing {
	t.Helper()
	listing, err := vmAdapter(t, map[string]string{"vm list": capture}).VMList(context.Background())
	if err != nil {
		t.Fatalf("VMList: %v", err)
	}
	return listing
}

func intPtr(n int) *int     { return &n }
func i64Ptr(n int64) *int64 { return &n }

// --- the listing ---------------------------------------------------------

func TestVMListRunningAndStopped(t *testing.T) {
	listing := vmList(t, "vm_guests_list")
	want := vms.Listing{VMBhyve: vms.VMBhyveReady, VMs: []vms.VM{
		{Name: "jd-cap-running", Datastore: "default", Loader: "uefi", CPU: "2", Memory: "512M",
			State: vms.StateRunning, PID: intPtr(16783)},
		{Name: "jd-cap-stopped", Datastore: "default", Loader: "uefi", CPU: "1", Memory: "256M",
			State: vms.StateStopped},
	}}
	if !reflect.DeepEqual(listing, want) {
		t.Errorf("listing = %+v\nwant      %+v", listing, want)
	}
}

// "Yes [1]" holds a space and widens the AUTO column, which moves STATE: read
// from both ends, it does not matter.
func TestVMListAutostart(t *testing.T) {
	listing := vmList(t, "vm_guests_autostart_list")
	running, stopped := listing.VMs[0], listing.VMs[1]
	if !running.Autostart || running.AutostartOrder != 1 || running.State != vms.StateRunning {
		t.Errorf("running = %+v, want autostart 1 and running", running)
	}
	if stopped.Autostart || stopped.AutostartOrder != 0 {
		t.Errorf("stopped = %+v, want no autostart", stopped)
	}
}

// vm-bhyve configured with no guests: the heading alone, and an empty list --
// never null.
func TestVMListNoGuests(t *testing.T) {
	listing := vmList(t, "vm_guests_none_list")
	if listing.VMBhyve != vms.VMBhyveReady || listing.VMs == nil || len(listing.VMs) != 0 {
		t.Errorf("listing = %+v, want ready and an empty list", listing)
	}
}

// Not installed and not configured are answers, the same ones Host Setup reads
// from the same command.
func TestVMListWhenVMBhyveCannotList(t *testing.T) {
	listing := vmList(t, "vm_installed_list")
	if listing.VMBhyve != vms.VMBhyveNotConfigured ||
		listing.Problem != "$vm_enable is not enabled in /etc/rc.conf!" || listing.VMs == nil {
		t.Errorf("listing = %+v, want not configured, vm-bhyve's words, an empty list", listing)
	}

	a := NewAdapter(&replayRunner{t: t, set: "rohan", captures: map[string]string{"vm list": "vm_list"}})
	listing, err := a.VMList(context.Background())
	if err != nil || listing.VMBhyve != vms.VMBhyveNotInstalled || listing.VMs == nil {
		t.Errorf("listing = %+v, err = %v; want not installed and no error", listing, err)
	}
}

// -v adds %CPU, RSZ and UPTIME. A heading Jail Deck did not measure is
// refused rather than read by position.
func TestVMListRefusesAnUnmeasuredHeading(t *testing.T) {
	_, err := vmAdapter(t, map[string]string{"vm list": "vm_guests_list_v"}).VMList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unrecognised heading") {
		t.Errorf("err = %v, want the heading refused", err)
	}
}

// Every state vm-bhyve can print, from its source, and column(1)'s merging of
// an empty field -- measured: an empty MEMORY shifts what follows under it.
func TestParseVMListEveryStateAndAMergedColumn(t *testing.T) {
	out := "NAME   DATASTORE  LOADER  CPU  MEMORY  VNC           AUTO     STATE\n" +
		"boot   default    grub    1    1G      -             No       Bootloader (812)\n" +
		"susp   default    uefi    1    1G      -             No       Suspended\n" +
		"shared nfs        uefi    2    2G      -             Yes [2]  Locked (orthanc)\n" +
		"gfx    default    uefi    2    2G      0.0.0.0:5900  No       Running (900)\n" +
		"half   default    uefi    2    -       No       Stopped\n" +
		"bare   default    none    -    No      Stopped\n"
	guests, err := parseVMList(out)
	if err != nil {
		t.Fatalf("parseVMList: %v", err)
	}
	want := []vms.VM{
		{Name: "boot", Datastore: "default", Loader: "grub", CPU: "1", Memory: "1G",
			State: vms.StateBootloader, PID: intPtr(812)},
		{Name: "susp", Datastore: "default", Loader: "uefi", CPU: "1", Memory: "1G",
			State: vms.StateSuspended},
		{Name: "shared", Datastore: "nfs", Loader: "uefi", CPU: "2", Memory: "2G",
			State: vms.StateLocked, LockedBy: "orthanc", Autostart: true, AutostartOrder: 2},
		{Name: "gfx", Datastore: "default", Loader: "uefi", CPU: "2", Memory: "2G",
			VNC: "0.0.0.0:5900", State: vms.StateRunning, PID: intPtr(900)},
		// One of CPU and MEMORY empty: four fields where five belong, and
		// which one is missing cannot be told.
		{Name: "half", Datastore: "default", Loader: "uefi", State: vms.StateStopped},
		// Both empty: three fields.
		{Name: "bare", Datastore: "default", Loader: "none", State: vms.StateStopped},
	}
	if !reflect.DeepEqual(guests, want) {
		t.Errorf("guests = %+v\nwant     %+v", guests, want)
	}
}

func TestParseVMListRefusesAnUnmeasuredRow(t *testing.T) {
	for _, row := range []string{
		"web   default    uefi    1    1G      -    No    Paused",
		"web   default    -    No    Stopped",
		"web   default    uefi    1    1G      -    extra    No    Stopped",
	} {
		out := "NAME  DATASTORE  LOADER  CPU  MEMORY  VNC  AUTO  STATE\n" + row + "\n"
		if _, err := parseVMList(out); err == nil || !strings.Contains(err.Error(), "unrecognised row") {
			t.Errorf("%q: err = %v, want the row refused", row, err)
		}
	}
}

// --- one guest -----------------------------------------------------------

func TestVMInfoRunning(t *testing.T) {
	a := vmAdapter(t, map[string]string{"vm info jd-cap-running": "vm_guests_info_running"})
	info, err := a.VMInfo(context.Background(), "jd-cap-running")
	if err != nil {
		t.Fatalf("VMInfo: %v", err)
	}
	want := vms.Info{
		UUID:           "ed117915-d146-11f1-b463-e80688cecc46",
		MemoryResident: i64Ptr(41967616),
		Consoles:       map[string]string{"com1": "/dev/nmdm-jd-cap-running.1B"},
		Networks: []vms.Network{{Number: 0, Emulation: "virtio-net", Switch: "jd-capsw",
			MAC: "58:9c:fc:02:fa:82", Device: "tap0", Bridge: "vm-jd-capsw",
			BytesIn: i64Ptr(0), BytesOut: i64Ptr(553)}},
		Disks: []vms.Disk{{Number: 0, DeviceType: "sparse-zvol", Emulation: "virtio-blk",
			Path: "/dev/zvol/tank/vm/jd-cap-running/disk0",
			Size: i64Ptr(1073741824), Used: i64Ptr(57344)}},
	}
	if !reflect.DeepEqual(info, want) {
		t.Errorf("info = %+v\nwant   %+v", info, want)
	}
}

// Stopped: no bhyve instance, so no resident memory and no console; no
// network configured, so none listed -- and an empty list, not null.
func TestVMInfoStopped(t *testing.T) {
	a := vmAdapter(t, map[string]string{"vm info jd-cap-stopped": "vm_guests_info_stopped"})
	info, err := a.VMInfo(context.Background(), "jd-cap-stopped")
	if err != nil {
		t.Fatalf("VMInfo: %v", err)
	}
	if info.MemoryResident != nil || info.Consoles != nil {
		t.Errorf("info = %+v, want no resident memory and no consoles", info)
	}
	if info.Networks == nil || len(info.Networks) != 0 || len(info.Disks) != 1 {
		t.Errorf("networks = %v, disks = %v; want none and one", info.Networks, info.Disks)
	}
}

func TestVMInfoNoSuchGuest(t *testing.T) {
	a := vmAdapter(t, map[string]string{"vm info jd-cap-nosuch": "vm_guests_info_absent"})
	if _, err := a.VMInfo(context.Background(), "jd-cap-nosuch"); !errors.Is(err, vms.ErrNoSuchVM) {
		t.Errorf("err = %v, want ErrNoSuchVM", err)
	}
}

// Sections not read are passed over: snapshots are zfs list's own lines, with
// tabs and timestamps that hold colons.
func TestParseVMInfoPassesOverWhatItDoesNotRead(t *testing.T) {
	out := "------------------------\nVirtual Machine: web\n------------------------\n" +
		"  state: stopped\n  uuid: abc\n  cpu-topology: sockets=1, cores=2\n\n" +
		"  virtual-disk\n    number: 0\n    system-path: /vm/web/disk0.img\n    bytes-size: 10 (10.000B)\n\n" +
		"  virtual-disk\n    number: 1\n    system-path: -\n\n" +
		"  snapshots\n    tank/vm/web@a\t1.2M\tMon Oct 26 10:15 2026\n\n" +
		"  clone-origin\n    tank/vm/base@x\n\n"
	info, err := parseVMInfo(out, "web")
	if err != nil {
		t.Fatalf("parseVMInfo: %v", err)
	}
	want := []vms.Disk{{Number: 0, Path: "/vm/web/disk0.img", Size: i64Ptr(10)}, {Number: 1}}
	if info.UUID != "abc" || !reflect.DeepEqual(info.Disks, want) {
		t.Errorf("info = %+v, want uuid abc and disks %+v", info, want)
	}
}

func TestParseVMInfoRefusesAnotherGuestsAnswer(t *testing.T) {
	if _, err := parseVMInfo("Virtual Machine: other\n  state: stopped\n", "web"); err == nil {
		t.Error("an answer about another guest was accepted")
	}
	if _, err := parseVMInfo("", "web"); err == nil {
		t.Error("an empty answer was accepted")
	}
}

// --- never a write -------------------------------------------------------

// The listing and the detail run vm list and vm info, and nothing else that
// vm-bhyve has: no start, stop, create, destroy, configure or console
// (JD-035).
func TestTheVMAdapterNeverWrites(t *testing.T) {
	var ran []system.Command
	runner := &replayRunner{t: t, set: vmSet, captures: map[string]string{
		"vm list":                "vm_guests_list",
		"vm info jd-cap-running": "vm_guests_info_running",
		"vm info jd-cap-stopped": "vm_guests_info_stopped",
	}}
	a := NewAdapter(runnerFunc(func(ctx context.Context, cmd system.Command) (system.CommandResult, error) {
		ran = append(ran, cmd)
		return runner.Run(ctx, cmd)
	}))
	service := vms.NewService(a)
	ctx := context.Background()
	if _, err := service.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, name := range []string{"jd-cap-running", "jd-cap-stopped"} {
		if _, err := service.Get(ctx, name); err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
	}
	if len(ran) < 5 {
		t.Fatalf("only %d commands ran; the check would prove little", len(ran))
	}
	for _, cmd := range ran {
		if cmd.Name != "vm" || len(cmd.Args) == 0 || (cmd.Args[0] != "list" && cmd.Args[0] != "info") {
			t.Errorf("ran %s, which is not a read", cmd)
		}
	}
}
