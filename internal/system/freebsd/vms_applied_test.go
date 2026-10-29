package freebsd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vms"
)

// From capture-vm-create-induced.sh (a guest made the way Jail Deck makes
// one: vm_mk_conf_networked, vm_mk_seed_files, vm_mk_datasets) and
// capture-vm-pin-induced.sh (vm_pin_*), on isengard. The files are laid out
// in a temporary datastore as vm-bhyve keeps them.

// appliedHost is a datastore at a temporary directory, with guest's
// configuration and seed written from the captures, and a runner that
// answers vm datastore list and disk0's zfs list.
func appliedHost(t *testing.T, guest, conf string, seed map[string]string) (*Adapter, *[]string, string) {
	t.Helper()
	dir := t.TempDir()
	if conf != "" {
		if err := os.MkdirAll(filepath.Join(dir, guest, ".cloud-init"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, guest, guest+".conf"), []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range seed {
		if err := os.WriteFile(filepath.Join(dir, guest, ".cloud-init", name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var ran []string
	disk := captured(t, "vm_mk_datasets")
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		ran = append(ran, cmd.String())
		switch {
		case cmd.String() == "vm datastore list":
			return system.CommandResult{Stdout: "NAME            TYPE        PATH                      ZFS DATASET\n" +
				"default         zfs         " + dir + "                 tank/vm\n"}, nil
		case cmd.String() == "zfs list -H -p -o volsize,origin tank/vm/jd-cap-made/disk0":
			// vm_mk_datasets' disk0 row, in the two columns asked for.
			for _, line := range strings.Split(disk, "\n") {
				f := strings.Split(line, "\t")
				if len(f) == 6 && strings.HasSuffix(f[0], "/disk0") {
					return system.CommandResult{Stdout: f[2] + "\t" + f[4] + "\n"}, nil
				}
			}
		case strings.HasPrefix(cmd.String(), "zfs list"):
			return system.CommandResult{Stderr: "cannot open '" + cmd.Args[len(cmd.Args)-1] + "': dataset does not exist\n",
				ExitCode: 1}, errors.New("exit status 1")
		case strings.HasPrefix(cmd.String(), "sysrc -f "), cmd.String() == "sysctl -n hw.ncpu":
			if cmd.Name == "sysctl" {
				return system.CommandResult{Stdout: captured(t, "vm_pin_ncpu")}, nil
			}
			return system.CommandResult{}, nil
		}
		t.Errorf("unexpected %s", cmd)
		return system.CommandResult{}, errors.New("unexpected")
	}))
	return a, &ran, dir
}

// captured is one capture's stdout from isengard-induced.
func captured(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hostfacts", "isengard-induced", name+".stdout"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// seedFiles splits vm_mk_seed_files, which printed "== <file>" before each.
func seedFiles(t *testing.T) map[string]string {
	files := map[string]string{}
	var name string
	for _, line := range strings.SplitAfter(captured(t, "vm_mk_seed_files"), "\n") {
		if strings.HasPrefix(line, "== ") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "== "))
			continue
		}
		files[name] += line
	}
	return files
}

func TestVMAppliedReadsAGuestMadeFromAnImage(t *testing.T) {
	seed := seedFiles(t)
	if len(seed) != 3 {
		t.Fatalf("seed files = %v", seed)
	}
	a, _, _ := appliedHost(t, "jd-cap-made", captured(t, "vm_mk_conf_networked"), seed)
	applied, err := a.VMApplied(context.Background(), "jd-cap-made")
	if err != nil {
		t.Fatal(err)
	}
	want := vms.Applied{
		CPU: "2", Memory: "1G", Switches: []string{"jd-capsw"},
		Origin: "tank/vm/.images/debian-13@base", DiskSize: 8589934592,
		Seed: true, Address: "10.77.0.10/24", Gateway: "10.77.0.1", Nameservers: []string{"10.77.0.1"},
		SSHKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFMz6uwq6uCySEimJJLIKB1qaSuK3GpjZ0BSxasBIfDG jd-capture"},
	}
	if !reflect.DeepEqual(applied, want) {
		t.Errorf("applied = %+v\nwant      %+v", applied, want)
	}
}

// Jail Deck's own user-data, for a console password (#152): the keys are
// read as before, and the hash beside them.
func TestVMAppliedReadsThePasswordHash(t *testing.T) {
	seed := seedFiles(t)
	seed["user-data"] = captured(t, "vm_pw_user_data")
	a, _, _ := appliedHost(t, "jd-cap-made", captured(t, "vm_mk_conf_networked"), seed)
	applied, err := a.VMApplied(context.Background(), "jd-cap-made")
	if err != nil {
		t.Fatal(err)
	}
	if applied.PasswordHash != "$2a$10$<hash>" || !reflect.DeepEqual(applied.SSHKeys, []string{"ssh-ed25519 <key> jd-live-pw"}) {
		t.Errorf("applied = %+v", applied)
	}
}

// vm-bhyve writes network-config only for vm create -n: a DHCP guest has
// user-data and meta-data, and nothing to read an address from.
func TestVMAppliedDHCPHasNoAddress(t *testing.T) {
	seed := seedFiles(t)
	delete(seed, "network-config")
	a, _, _ := appliedHost(t, "jd-cap-made", captured(t, "vm_mk_conf_networked"), seed)
	applied, err := a.VMApplied(context.Background(), "jd-cap-made")
	if err != nil || !applied.Seed || applied.Address != "" || applied.Gateway != "" || applied.Nameservers != nil {
		t.Errorf("applied = %+v, %v", applied, err)
	}
}

// vm_pin_conf: a template-style configuration (cpu=2 unquoted), the
// network0_mac vm create generates without a NIC, and the pin sysrc wrote.
func TestVMAppliedReadsThePin(t *testing.T) {
	a, _, _ := appliedHost(t, "jd-cap-pin", captured(t, "vm_pin_conf"), nil)
	applied, err := a.VMApplied(context.Background(), "jd-cap-pin")
	if err != nil {
		t.Fatal(err)
	}
	if applied.CPU != "2" || applied.Memory != "256M" || applied.BhyveOptions != "-p 0:2 -p 1:3" ||
		applied.Switches != nil || applied.Seed || applied.Origin != "" || applied.DiskSize != 0 {
		t.Errorf("applied = %+v", applied)
	}
}

func TestVMAppliedOutsideTheDefaultDatastore(t *testing.T) {
	a, _, _ := appliedHost(t, "jd-cap-made", "", nil)
	if _, err := a.VMApplied(context.Background(), "elsewhere"); !errors.Is(err, vms.ErrNoSuchVM) {
		t.Errorf("err = %v, want ErrNoSuchVM", err)
	}
}

func TestVMAppliedRefusesASeedItCannotRead(t *testing.T) {
	seed := seedFiles(t)
	seed["user-data"] = "ssh_authorized_keys: [unclosed\n"
	a, _, _ := appliedHost(t, "jd-cap-made", captured(t, "vm_mk_conf_networked"), seed)
	if _, err := a.VMApplied(context.Background(), "jd-cap-made"); err == nil || !strings.Contains(err.Error(), "user-data") {
		t.Errorf("err = %v, want one naming user-data", err)
	}
}

func TestVMPinCPUsIsSysrcOnTheGuestsConfiguration(t *testing.T) {
	a, ran, dir := appliedHost(t, "jd-cap-pin", captured(t, "vm_pin_conf_unset"), nil)
	command, err := a.VMPinCPUs(context.Background(), "jd-cap-pin", []int{2, 3})
	want := "sysrc -f " + filepath.Join(dir, "jd-cap-pin", "jd-cap-pin.conf") + " bhyve_options=-p 0:2 -p 1:3"
	if err != nil || command != want || !slices.Contains(*ran, want) {
		t.Errorf("command = %q, ran %v, err %v\nwant %q", command, *ran, err, want)
	}
}

func TestHostCPUs(t *testing.T) {
	a, _, _ := appliedHost(t, "x", "", nil)
	if n, err := a.HostCPUs(context.Background()); err != nil || n != 16 {
		t.Errorf("HostCPUs = %d, %v; want isengard's 16", n, err)
	}
	bad := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{Stdout: "0\n"}, nil
	}))
	if _, err := bad.HostCPUs(context.Background()); err == nil {
		t.Error("hw.ncpu 0 was accepted")
	}
}

func TestParseVMConf(t *testing.T) {
	got := parseVMConf("# a comment\nloader=\"uefi\"\ncpu=2\n  memory=\"1G\"\nbhyve_options=\"-p 0:2 -p 1:3\"\nnot a setting\nempty=\"\"\n")
	want := map[string]string{"loader": "uefi", "cpu": "2", "memory": "1G", "bhyve_options": "-p 0:2 -p 1:3", "empty": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("conf = %v", got)
	}
}

// Constructed: disk0 a plain zvol, not a clone, which zfs prints with "-" for
// its origin -- the form of vm_mk_datasets' guest dataset row.
func TestVMAppliedADiskThatIsNotAClone(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain", "plain.conf"), []byte("cpu=1\nmemory=512M\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		if cmd.String() == "vm datastore list" {
			return system.CommandResult{Stdout: "NAME TYPE PATH ZFS\ndefault zfs " + dir + " tank/vm\n"}, nil
		}
		return system.CommandResult{Stdout: "1073741824\t-\n"}, nil
	}))
	applied, err := a.VMApplied(context.Background(), "plain")
	if err != nil || applied.Origin != "" || applied.DiskSize != 1073741824 {
		t.Errorf("applied = %+v, %v; want no origin and the size", applied, err)
	}
}
