package freebsd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vms"
)

// From capture-vm-create-induced.sh on isengard: one guest made from the
// debian-13 image on a throwaway switch, reached over SSH, and destroyed.

// Constructed: the default datastore as isengard's is, in the format of
// vm_ready_datastore. The creation capture did not list it.
var isengardDatastore = map[string]system.CommandResult{
	"vm datastore list": {Stdout: "NAME            TYPE        PATH                      ZFS DATASET\n" +
		"default         zfs         /vm                       tank/vm\n"},
}

// createAdapter replays captures, answers the datastore listing, keeps every
// command, and points the template and keys files at temporary directories.
func createAdapter(t *testing.T, captures map[string]string) (*Adapter, *imageRunner, string, string) {
	t.Helper()
	runner := &imageRunner{replay: &replayRunner{t: t, set: vmSet, captures: captures}, constructed: isengardDatastore}
	a := NewAdapter(runner)
	a.vmTemplateDir, a.vmScratchDir = t.TempDir(), t.TempDir()
	return a, runner, a.vmTemplateDir, a.vmScratchDir
}

var madeSpec = vms.CreateSpec{
	Name: "jd-cap-made", CPU: 2, Memory: "1G", Disk: "8G",
	Address: "10.77.0.10/24", Gateway: "10.77.0.1", Nameservers: []string{"10.77.0.1"},
	SSHKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFMz6uwq6uCySEimJJLIKB1qaSuK3GpjZ0BSxasBIfDG jd-capture"},
}

func TestVMSwitches(t *testing.T) {
	a, _, _, _ := createAdapter(t, map[string]string{"vm switch list": "vm_mk_switch_list"})
	switches, err := a.VMSwitches(context.Background())
	if err != nil || !slices.Equal(switches, []string{"jd-capsw"}) {
		t.Errorf("switches = %v, %v", switches, err)
	}

	// Constructed: isengard with no switch printed the header alone.
	none := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{Stdout: "NAME  TYPE  IFACE  ADDRESS  PRIVATE  MTU  VLAN  PORTS\n"}, nil
	}))
	if switches, err := none.VMSwitches(context.Background()); err != nil || switches == nil || len(switches) != 0 {
		t.Errorf("switches = %#v, %v; want an empty list", switches, err)
	}
}

func TestVMCreateWritesTheTemplateAndPassesTheSeed(t *testing.T) {
	keysFile := ""
	var keysSeen string
	a, runner, templates, scratch := createAdapter(t, nil)
	keysFile = filepath.Join(scratch, "jaildeck-jd-cap-made.keys")
	want := "vm create -t jaildeck -c 2 -m 1G -s 8G -C -k " + keysFile +
		" -n ip=10.77.0.10/24;gateway4=10.77.0.1;nameservers=10.77.0.1 jd-cap-made"
	runner.replay.captures = map[string]string{want: "vm_mk_create"}
	// The keys file has to be there while vm create reads it.
	a.runner = runnerFunc(func(ctx context.Context, cmd system.Command) (system.CommandResult, error) {
		if cmd.String() == want {
			b, _ := os.ReadFile(keysFile)
			keysSeen = string(b)
		}
		return runner.Run(ctx, cmd)
	})

	command, err := a.VMCreate(context.Background(), madeSpec)
	if err != nil || command != want {
		t.Fatalf("VMCreate = %q, %v\nwant %q", command, err, want)
	}
	if keysSeen != madeSpec.SSHKeys[0]+"\n" {
		t.Errorf("vm create read keys %q", keysSeen)
	}
	if _, err := os.Stat(keysFile); !os.IsNotExist(err) {
		t.Error("the keys file outlived vm create")
	}
	template, err := os.ReadFile(filepath.Join(templates, "jaildeck.conf"))
	if err != nil || string(template) != vmTemplate {
		t.Errorf("template = %q, %v", template, err)
	}
	for _, line := range []string{`loader="uefi"`, `disk0_dev="sparse-zvol"`} {
		if !strings.Contains(vmTemplate, line) {
			t.Errorf("the template has no %s", line)
		}
	}
	if strings.Contains(vmTemplate, "network0") {
		t.Error("the template has a NIC; it is added on the request's switch")
	}
}

func TestVMCreateWithNoKeysAndDHCPPassesNeither(t *testing.T) {
	a, runner, _, _ := createAdapter(t, nil)
	want := "vm create -t jaildeck -c 1 -m 1G -s 3221225472 -C plain"
	runner.replay.captures = map[string]string{want: "vm_mk_create"}
	if _, err := a.VMCreate(context.Background(), vms.CreateSpec{Name: "plain", CPU: 1, Memory: "1G", Disk: "3221225472"}); err != nil {
		t.Fatal(err)
	}
}

func TestVMCreateSaysWhatVMBhyveRefused(t *testing.T) {
	a, runner, _, _ := createAdapter(t, nil)
	runner.replay.captures = map[string]string{"vm create -t jaildeck -c 1 -m 1G -s 8G -C jd-cap-made": "vm_mk_create_taken"}
	_, err := a.VMCreate(context.Background(), vms.CreateSpec{Name: "jd-cap-made", CPU: 1, Memory: "1G", Disk: "8G"})
	if err == nil || !strings.Contains(err.Error(), "virtual machine already exists in /vm/jd-cap-made") {
		t.Errorf("err = %v", err)
	}
}

func TestWriteIfChangedLeavesARightFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jaildeck.conf")
	if err := writeIfChanged(path, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	// Unwritable now, so a second write of the same content must not try.
	if err := writeIfChanged(path, []byte("a")); err != nil {
		t.Errorf("rewrote an unchanged file: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeIfChanged(path, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "b" {
		t.Errorf("file = %q, want the new content", b)
	}
}

func TestNetconfig(t *testing.T) {
	cases := map[string]vms.CreateSpec{
		"":                                   {},
		"ip=10.0.0.5/24":                     {Address: "10.0.0.5/24"},
		"ip=2001:db8::5/64;gateway6=fe80::1": {Address: "2001:db8::5/64", Gateway: "fe80::1"},
		"ip=10.0.0.5/24;gateway4=10.0.0.1;nameservers=10.0.0.1,9.9.9.9": {
			Address: "10.0.0.5/24", Gateway: "10.0.0.1", Nameservers: []string{"10.0.0.1", "9.9.9.9"}},
	}
	for want, spec := range cases {
		if got := netconfig(spec); got != want {
			t.Errorf("netconfig(%+v) = %q, want %q", spec, got, want)
		}
	}
}

func TestVMAddNetwork(t *testing.T) {
	a, _, _, _ := createAdapter(t, map[string]string{"vm add -d network -s jd-capsw jd-cap-made": "vm_mk_add_network"})
	if command, err := a.VMAddNetwork(context.Background(), "jd-cap-made", "jd-capsw"); err != nil ||
		command != "vm add -d network -s jd-capsw jd-cap-made" {
		t.Errorf("VMAddNetwork = %q, %v", command, err)
	}
}

func TestVMReplaceDiskClonesInDevMode(t *testing.T) {
	a, runner, _, _ := createAdapter(t, map[string]string{
		"zfs destroy tank/vm/jd-cap-made/disk0": "vm_mk_disk_destroy",
		"zfs clone -o volmode=dev -o volsize=8589934592 tank/vm/.images/debian-13@base tank/vm/jd-cap-made/disk0": "vm_mk_disk_clone",
	})
	command, err := a.VMReplaceDisk(context.Background(), "jd-cap-made", "tank/vm/.images/debian-13@base", 8<<30)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"vm datastore list", "zfs destroy tank/vm/jd-cap-made/disk0",
		"zfs clone -o volmode=dev -o volsize=8589934592 tank/vm/.images/debian-13@base tank/vm/jd-cap-made/disk0"}
	if got := runner.lines(); !slices.Equal(got, want) {
		t.Errorf("ran %v", got)
	}
	if command != want[1]+" && "+want[2] {
		t.Errorf("command = %q", command)
	}
}

// Constructed: a datastore that is a plain directory has no dataset to
// clone into.
func TestVMReplaceDiskNeedsZFS(t *testing.T) {
	a := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{Stdout: "NAME  TYPE  PATH  ZFS DATASET\ndefault  directory  /vm  -\n"}, nil
	}))
	if _, err := a.VMReplaceDisk(context.Background(), "web", "tank/vm/.images/debian-13@base", 8<<30); err == nil ||
		!strings.Contains(err.Error(), "not on ZFS") {
		t.Errorf("err = %v", err)
	}
}

func TestVMDestroy(t *testing.T) {
	a, _, _, _ := createAdapter(t, map[string]string{"vm destroy -f jd-cap-made": "vm_mk_destroy"})
	if command, err := a.VMDestroy(context.Background(), "jd-cap-made"); err != nil || command != "vm destroy -f jd-cap-made" {
		t.Errorf("VMDestroy = %q, %v", command, err)
	}
	running, _, _, _ := createAdapter(t, map[string]string{"vm destroy -f jd-cap-made": "vm_mk_destroy_running"})
	if _, err := running.VMDestroy(context.Background(), "jd-cap-made"); err == nil ||
		!strings.Contains(err.Error(), "appears to be running on isengard") {
		t.Errorf("err = %v", err)
	}
}
