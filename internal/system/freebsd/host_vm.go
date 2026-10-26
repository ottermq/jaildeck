package freebsd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/system"
)

// defaultUEFIFirmware is what vm-bhyve's loader="uefi" boots a guest with,
// installed by edk2-bhyve.
const defaultUEFIFirmware = "/usr/local/share/uefi-firmware/BHYVE_UEFI.fd"

// vmTimeout bounds a vm-bhyve read. `vm list` reads rc.conf and the
// datastore's configuration files; it returns in well under a second.
const vmTimeout = 15 * time.Second

// VMBhyve asks vm-bhyve whether it can run, with the read every vm-bhyve
// command starts with: `vm list` checks vm_enable, vm_dir and the datastore
// before it lists anything, and says which one is wrong.
//
// Measured on isengard, in every state on the way to a usable vm-bhyve:
//
//	not installed       `vm` not found
//	installed           ERROR: $vm_enable is not enabled in /etc/rc.conf!
//	enabled, no vm_dir  ERROR: $vm_dir has not been configured or is not a valid directory
//	vm_dir, no dataset  ERROR: unable to locate mountpoint for ZFS dataset tank/jd-vmcap
//	dataset unmounted   (the same line)
//	mounted             exit 0, before `vm init` as well as after
//
// Each is exit 1 with that line on stderr, which is the answer here rather
// than a failure to ask.
func (a *Adapter) VMBhyve(ctx context.Context) (host.VMBhyve, error) {
	cmd := system.Command{Name: "vm", Args: []string{"list"}, Timeout: vmTimeout}
	result, err := a.runner.Run(ctx, cmd)
	if errors.Is(err, system.ErrCommandNotFound) {
		// Not installed, or not on the PATH rc gave the service -- the
		// rc.d script adds /usr/local/sbin for exactly this (#95).
		return host.VMBhyve{}, nil
	}
	if err != nil {
		if exited(result, err) {
			if problem := vmProblem(result.Stdout, result.Stderr); problem != "" {
				return host.VMBhyve{Installed: true, Problem: problem}, nil
			}
		}
		if msg := strings.TrimSpace(result.Stderr); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return host.VMBhyve{}, &system.CommandError{Command: cmd.Name, Args: cmd.Args, Result: result, Err: err}
	}

	vm := host.VMBhyve{Installed: true}
	stores, err := a.runHost(ctx, system.Command{Name: "vm", Args: []string{"datastore", "list"}, Timeout: vmTimeout})
	if err != nil {
		// vm list answered, so vm-bhyve works; where its datastore is was
		// only ever going to be the observation beside the pass.
		return vm, nil
	}
	// "NAME  TYPE  PATH  ZFS DATASET", then "default  zfs  /jd-vmcap  tank/jd-vmcap"
	for _, line := range nonEmptyLines(stores.Stdout) {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "default" {
			vm.Datastore = fields[2]
			if len(fields) >= 4 {
				vm.Dataset = fields[3]
			}
		}
	}
	return vm, nil
}

// vmProblem is vm-bhyve's own message for why it stopped.
//
// It has two ways to say so, both of which exit 1 straight after (lib/vm-util,
// read in the source): util::err prints "<path>/vm: ERROR: <message>" to
// stderr, and util::err_inline prints "  ! <message>" to stdout, among
// informational output. Every `vm list` failure measured was the first kind;
// the second is read too, so that one would not be reported as a bare exit
// status. A WARNING line can come before an ERROR -- "$vm_enable is not set
// properly" does -- and is not the answer.
func vmProblem(stdout, stderr string) string {
	for _, line := range nonEmptyLines(stderr) {
		if _, message, found := strings.Cut(line, "ERROR: "); found {
			return strings.TrimSpace(message)
		}
	}
	for _, line := range nonEmptyLines(stdout) {
		if message, found := strings.CutPrefix(strings.TrimSpace(line), "! "); found {
			return strings.TrimSpace(message)
		}
	}
	return ""
}

// UEFIFirmware reports where edk2-bhyve's firmware is, or "" when it is not
// installed. A file that is there is the whole question; reading it is not.
func (a *Adapter) UEFIFirmware(ctx context.Context) (string, error) {
	_, err := os.Stat(a.uefiFirmware)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return a.uefiFirmware, nil
}

var _ host.HostSystem = (*Adapter)(nil)
