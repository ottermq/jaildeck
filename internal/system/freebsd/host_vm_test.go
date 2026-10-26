package freebsd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ottermq/jaildeck/internal/host"
)

// Every state on the way to a usable vm-bhyve, captured on isengard by
// capture-vm-induced.sh: installed, enabled, a vm_dir naming a dataset that
// does not exist, one that is not mounted, and one that is (see
// testdata/hostfacts/README.md).

func vmBhyveIn(t *testing.T, list string) host.VMBhyve {
	t.Helper()
	a := NewAdapter(&replayRunner{t: t, set: "isengard-induced", captures: map[string]string{
		"vm list":           list,
		"vm datastore list": "vm_ready_datastore",
	}})
	vm, err := a.VMBhyve(context.Background())
	if err != nil {
		t.Fatalf("VMBhyve: %v", err)
	}
	return vm
}

// vm-bhyve's own words, from its ERROR line. The WARNING before the first one
// is not the answer.
func TestVMBhyveReportsItsOwnProblem(t *testing.T) {
	for _, tt := range []struct{ capture, want string }{
		{"vm_installed_list", "$vm_enable is not enabled in /etc/rc.conf!"},
		{"vm_enabled_nodir_list", "$vm_dir has not been configured or is not a valid directory"},
		{"vm_dir_missing_list", "unable to locate mountpoint for ZFS dataset tank/jd-vmcap"},
		// The same line for a dataset that exists and is not mounted.
		{"vm_unmounted_list", "unable to locate mountpoint for ZFS dataset tank/jd-vmcap"},
	} {
		t.Run(tt.capture, func(t *testing.T) {
			vm := vmBhyveIn(t, tt.capture)
			if !vm.Installed || vm.Problem != tt.want {
				t.Errorf("vm = %+v, want installed with problem %q", vm, tt.want)
			}
		})
	}
}

// Configured and answering, before vm init as well as after -- measured --
// with its default datastore read for the observation.
func TestVMBhyveConfigured(t *testing.T) {
	for _, capture := range []string{"vm_uninit_list", "vm_ready_list"} {
		t.Run(capture, func(t *testing.T) {
			vm := vmBhyveIn(t, capture)
			want := host.VMBhyve{Installed: true, Datastore: "/jd-vmcap", Dataset: "tank/jd-vmcap"}
			if vm != want {
				t.Errorf("vm = %+v, want %+v", vm, want)
			}
		})
	}
}

// Not installed: `vm` is not on the PATH, which is an answer.
func TestVMBhyveNotInstalled(t *testing.T) {
	for _, set := range []string{"isengard", "rohan", "rivendell", "vm-15.1"} {
		t.Run(set, func(t *testing.T) {
			a := NewAdapter(&replayRunner{t: t, set: set, captures: map[string]string{"vm list": "vm_list"}})
			vm, err := a.VMBhyve(context.Background())
			if err != nil || vm.Installed {
				t.Errorf("vm = %+v, err = %v; want not installed and no error", vm, err)
			}
		})
	}
}

func TestVMProblem(t *testing.T) {
	stderr := "/usr/local/sbin/vm: WARNING: $vm_enable is not set properly - see rc.conf(5).\n" +
		"/usr/local/sbin/vm: ERROR: $vm_enable is not enabled in /etc/rc.conf!\n"
	if got := vmProblem("", stderr); got != "$vm_enable is not enabled in /etc/rc.conf!" {
		t.Errorf("vmProblem = %q", got)
	}
	if got := vmProblem("", "/usr/local/sbin/vm: WARNING: only a warning\n"); got != "" {
		t.Errorf("a warning alone became a problem: %q", got)
	}
	// util::err_inline's form, from the source rather than a capture: on
	// stdout, among informational lines.
	if got := vmProblem("Starting web1\n  * found guest\n  ! guest is already running\n", ""); got != "guest is already running" {
		t.Errorf("inline problem = %q", got)
	}
}

func TestUEFIFirmware(t *testing.T) {
	dir := t.TempDir()
	a := NewAdapter(&replayRunner{t: t})
	a.uefiFirmware = filepath.Join(dir, "BHYVE_UEFI.fd")
	if path, err := a.UEFIFirmware(context.Background()); err != nil || path != "" {
		t.Errorf("absent firmware = %q, %v; want empty and no error", path, err)
	}
	if err := os.WriteFile(a.uefiFirmware, []byte("firmware"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, err := a.UEFIFirmware(context.Background()); err != nil || path != a.uefiFirmware {
		t.Errorf("present firmware = %q, %v", path, err)
	}
}

// A stat that fails for any reason but "not there" is not "not installed":
// it is an answer the check cannot give, and becomes unknown.
func TestUEFIFirmwareUnreadableIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads through a mode-0 directory")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	a := NewAdapter(&replayRunner{t: t})
	a.uefiFirmware = filepath.Join(dir, "BHYVE_UEFI.fd")
	if _, err := a.UEFIFirmware(context.Background()); err == nil {
		t.Error("an unreadable firmware path was reported as absent")
	}
}
