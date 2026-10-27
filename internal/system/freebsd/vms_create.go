package freebsd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vms"
)

// vmTemplateName is Jail Deck's own vm-bhyve template, in the datastore's
// .templates. vm create needs one, and vm-bhyve's default.conf boots with
// bhyveload from a file disk, which a cloud image is not.
const vmTemplateName = "jaildeck"

// vmTemplate is that template: UEFI, one virtio disk, and no NIC. CPU and
// memory are overridden on vm create's command line, the disk is replaced by
// a clone of an image, and the NIC is added on the switch the request names
// (JD-038). Measured with exactly this on isengard.
const vmTemplate = `# Jail Deck's template for guests made from an image (JD-038).
# Rewritten by Jail Deck when it differs; edits here do not last.
loader="uefi"
cpu=1
memory=512M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
`

// vmCreateTimeout bounds vm create, which made the guest's dataset, zvol and
// seed in 2 seconds, measured.
const vmCreateTimeout = time.Minute

// VMSwitches reads the names out of `vm switch list`:
//
//	NAME      TYPE      IFACE        ADDRESS       PRIVATE  MTU  VLAN  PORTS
//	jd-capsw  standard  vm-jd-capsw  10.77.0.1/24  no       -    -     -
func (a *Adapter) VMSwitches(ctx context.Context) ([]string, error) {
	cmd := system.Command{Name: "vm", Args: []string{"switch", "list"}, Timeout: vmTimeout}
	result, err := a.runner.Run(ctx, cmd)
	if err != nil {
		return nil, vmCommandError(cmd, result, err)
	}
	names := make([]string, 0)
	for i, line := range nonEmptyLines(result.Stdout) {
		fields := strings.Fields(line)
		if i == 0 && len(fields) > 0 && fields[0] == "NAME" {
			continue
		}
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	return names, nil
}

// VMCreate writes Jail Deck's template if it is not already there as it
// should be, then runs
//
//	vm create -t jaildeck -c <cpu> -m <memory> -s <disk> -C [-k <keys>] [-n <netconfig>] <name>
//
// vm-bhyve writes the cloud-init seed's files from these at once, and builds
// seed.iso from them at the guest's first start, measured. The keys file
// lives only for the command: vm create copies the keys into the seed.
func (a *Adapter) VMCreate(ctx context.Context, spec vms.CreateSpec) (string, error) {
	dir, _, err := a.vmDatastore(ctx)
	if err != nil {
		return "", err
	}
	templates := a.vmTemplateDir
	if templates == "" {
		templates = filepath.Join(dir, ".templates")
	}
	if err := writeIfChanged(filepath.Join(templates, vmTemplateName+".conf"), []byte(vmTemplate)); err != nil {
		return "", fmt.Errorf("writing Jail Deck's vm-bhyve template: %w", err)
	}

	args := []string{"create", "-t", vmTemplateName, "-c", strconv.Itoa(spec.CPU), "-m", spec.Memory,
		"-s", spec.Disk, "-C"}
	if len(spec.SSHKeys) > 0 {
		keys := filepath.Join(a.vmScratchDir, "jaildeck-"+spec.Name+".keys")
		if err := os.WriteFile(keys, []byte(strings.Join(spec.SSHKeys, "\n")+"\n"), 0o600); err != nil {
			return "", fmt.Errorf("writing the guest's SSH keys for vm create: %w", err)
		}
		defer os.Remove(keys)
		args = append(args, "-k", keys)
	}
	if net := netconfig(spec); net != "" {
		args = append(args, "-n", net)
	}
	args = append(args, spec.Name)
	return a.vmLifecycle(ctx, system.Command{Name: "vm", Args: args, Timeout: vmCreateTimeout})
}

// netconfig is vm create -n's form: key=value pairs separated by ';'. The
// guest's name is already its hostname, which vm-bhyve defaults to. vm-bhyve
// refuses a key it does not know ("invalid netconfig option - 'mtu'",
// measured), and every value here was parsed as an address first.
func netconfig(spec vms.CreateSpec) string {
	if spec.Address == "" {
		return ""
	}
	pairs := []string{"ip=" + spec.Address}
	if spec.Gateway != "" {
		key := "gateway4"
		if strings.Contains(spec.Gateway, ":") {
			key = "gateway6"
		}
		pairs = append(pairs, key+"="+spec.Gateway)
	}
	if len(spec.Nameservers) > 0 {
		pairs = append(pairs, "nameservers="+strings.Join(spec.Nameservers, ","))
	}
	return strings.Join(pairs, ";")
}

// VMAddNetwork runs `vm add -d network -s <switch> <name>`. It becomes
// network0, beside the network0_mac vm create already generated, which is
// the MAC the seed's network-config matches the NIC by -- measured: eth0 came
// up with the seed's address. vm add does not check that the switch exists
// (exit 0 for jd-nosuchsw, measured); the service has already.
func (a *Adapter) VMAddNetwork(ctx context.Context, name, vmSwitch string) (string, error) {
	return a.vmLifecycle(ctx, system.Command{Name: "vm", Args: []string{"add", "-d", "network", "-s", vmSwitch, name},
		Timeout: vmTimeout})
}

// VMReplaceDisk swaps disk0 for a clone of the image (JD-037):
//
//	zfs destroy <datastore>/<name>/disk0
//	zfs clone -o volmode=dev -o volsize=<size> <snapshot> <datastore>/<name>/disk0
//
// volmode=dev is not optional: a clone grown past its image in GEOM's
// default mode is held open by the kernel's GPT auto-resize, and the guest
// on it boots nothing.
func (a *Adapter) VMReplaceDisk(ctx context.Context, name, snapshot string, size int64) (string, error) {
	_, dataset, err := a.vmDatastore(ctx)
	if err != nil {
		return "", err
	}
	if dataset == "" {
		return "", fmt.Errorf("vm-bhyve's default datastore is not on ZFS, so the guest's disk cannot be a clone")
	}
	disk := dataset + "/" + name + "/disk0"
	destroy := system.Command{Name: "zfs", Args: []string{"destroy", disk}}
	clone := system.Command{Name: "zfs", Args: []string{"clone", "-o", "volmode=dev",
		"-o", "volsize=" + strconv.FormatInt(size, 10), snapshot, disk}}
	command := destroy.String() + " && " + clone.String()
	if _, err := a.runHost(ctx, destroy); err != nil {
		return command, err
	}
	_, err = a.runHost(ctx, clone)
	return command, err
}

// VMDestroy runs `vm destroy -f <name>`: -f because without it vm-bhyve asks
// on its terminal. It destroys the guest's dataset recursively, its disk
// clone with it. A running guest is refused with exit 1 and "appears to be
// running on <host> (locked)", measured.
func (a *Adapter) VMDestroy(ctx context.Context, name string) (string, error) {
	return a.vmLifecycle(ctx, system.Command{Name: "vm", Args: []string{"destroy", "-f", name},
		Timeout: vmCreateTimeout})
}

// vmDatastore is the default datastore's directory and ZFS dataset.
func (a *Adapter) vmDatastore(ctx context.Context) (dir, dataset string, err error) {
	result, err := a.runHost(ctx, system.Command{Name: "vm", Args: []string{"datastore", "list"}, Timeout: vmTimeout})
	if err != nil {
		return "", "", err
	}
	dir, dataset = defaultDatastore(result.Stdout)
	if dir == "" {
		return "", "", fmt.Errorf("vm datastore list names no default datastore")
	}
	return dir, dataset, nil
}

// writeIfChanged writes a file only when its content differs, so a template
// that is already right is not touched on every create.
func writeIfChanged(path string, content []byte) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, content) {
		return nil
	}
	return os.WriteFile(path, content, 0o644)
}
