package freebsd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vms"
)

// vmListHeader is `vm list`'s heading, without -v. Anything else -- -v's
// extra columns, or a vm-bhyve that changed them -- is refused rather than
// read by position.
var vmListHeader = []string{"NAME", "DATASTORE", "LOADER", "CPU", "MEMORY", "VNC", "AUTO", "STATE"}

// vmListRowRE takes a row of `vm list` apart from both ends. The name cannot
// hold a space (util::check_name); AUTO and STATE can -- "Yes [1]",
// "Running (16783)" -- and are the last two columns, so they are matched
// whole at the end, in every form vm-bhyve can print (lib/vm-core,
// lib/vm-run). What is left between is the five columns that hold no space.
var vmListRowRE = regexp.MustCompile(
	`^(\S+)\s+(.*?)\s+(No|Yes \[(\d+)\])\s+` +
		`(Running \((\d+)\)|Bootloader \((\d+)\)|Stopped|Suspended|Locked \((.+)\))$`)

// VMList reads `vm list`, the same command Host Setup's readiness check runs:
// vm-bhyve checks vm_enable, vm_dir and its datastore before listing
// anything, and a host where it cannot is answered here as the listing,
// not as an error (JD-035).
func (a *Adapter) VMList(ctx context.Context) (vms.Listing, error) {
	cmd := system.Command{Name: "vm", Args: []string{"list"}, Timeout: vmTimeout}
	result, err := a.runner.Run(ctx, cmd)
	if errors.Is(err, system.ErrCommandNotFound) {
		return vms.Listing{VMBhyve: vms.VMBhyveNotInstalled, VMs: make([]vms.VM, 0)}, nil
	}
	if err != nil {
		if exited(result, err) {
			if problem := vmProblem(result.Stdout, result.Stderr); problem != "" {
				return vms.Listing{VMBhyve: vms.VMBhyveNotConfigured, Problem: problem,
					VMs: make([]vms.VM, 0)}, nil
			}
		}
		return vms.Listing{}, vmCommandError(cmd, result, err)
	}
	guests, err := parseVMList(result.Stdout)
	if err != nil {
		return vms.Listing{}, err
	}
	return vms.Listing{VMBhyve: vms.VMBhyveReady, VMs: guests}, nil
}

// parseVMList reads `vm list`'s table.
//
// The table is column(1)'s, and column(1) on FreeBSD merges an empty field
// into the next -- measured: an empty MEMORY shifts AUTO under it. Of the
// five middle columns only CPU and MEMORY can be empty (LOADER defaults to
// "none", VNC to "-"), so with fewer than five the two are left unknown
// rather than guessed between.
func parseVMList(out string) ([]vms.VM, error) {
	guests := make([]vms.VM, 0)
	header := false
	for _, line := range nonEmptyLines(out) {
		if !header {
			if !slices.Equal(strings.Fields(line), vmListHeader) {
				return nil, fmt.Errorf("vm list: unrecognised heading %q", line)
			}
			header = true
			continue
		}
		m := vmListRowRE.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("vm list: unrecognised row %q", line)
		}
		middle := strings.Fields(m[2])
		if len(middle) < 3 || len(middle) > 5 {
			return nil, fmt.Errorf("vm list: unrecognised row %q", line)
		}
		vm := vms.VM{
			Name:      m[1],
			Datastore: middle[0],
			Loader:    middle[1],
			VNC:       dashless(middle[len(middle)-1]),
		}
		if len(middle) == 5 {
			vm.CPU, vm.Memory = middle[2], middle[3]
		}
		if m[4] != "" {
			vm.Autostart = true
			vm.AutostartOrder, _ = strconv.Atoi(m[4])
		}
		switch state := m[5]; {
		case m[6] != "":
			vm.State, vm.PID = vms.StateRunning, atoiPtr(m[6])
		case m[7] != "":
			vm.State, vm.PID = vms.StateBootloader, atoiPtr(m[7])
		case m[8] != "":
			vm.State, vm.LockedBy = vms.StateLocked, m[8]
		default:
			vm.State = strings.ToLower(state)
		}
		guests = append(guests, vm)
	}
	if !header {
		return nil, errors.New("vm list: no heading")
	}
	return guests, nil
}

// VMInfo reads `vm info <name>`. The name is an argument, never through a
// shell, and the service only asks about a name the listing has.
func (a *Adapter) VMInfo(ctx context.Context, name string) (vms.Info, error) {
	cmd := system.Command{Name: "vm", Args: []string{"info", name}, Timeout: vmTimeout}
	result, err := a.runner.Run(ctx, cmd)
	if err != nil {
		// Measured: "ERROR: unable to locate virtual machine 'x'", exit 1.
		if exited(result, err) &&
			strings.HasPrefix(vmProblem(result.Stdout, result.Stderr), "unable to locate virtual machine") {
			return vms.Info{}, vms.ErrNoSuchVM
		}
		return vms.Info{}, vmCommandError(cmd, result, err)
	}
	return parseVMInfo(result.Stdout, name)
}

// parseVMInfo reads one guest's block of `vm info`:
//
//	------------------------
//	Virtual Machine: jd-cap-running
//	------------------------
//	  state: running (16783)
//	  uuid: ed117915-...
//	  memory-resident: 41967616 (40.023M)
//
//	  console-ports
//	    com1: /dev/nmdm-jd-cap-running.1B
//
//	  network-interface
//	    number: 0
//	    ...
//
// A key is two spaces in, a section heading two spaces in with no colon, and
// a section's keys four. The rules of dashes read as headings of no section. network-interface and virtual-disk repeat, once per
// interface or disk. Sections not read here -- snapshots, clone-origin -- are
// passed over, and so is any key vm-bhyve adds.
func parseVMInfo(out, name string) (vms.Info, error) {
	info := vms.Info{Networks: make([]vms.Network, 0), Disks: make([]vms.Disk, 0)}
	found := false
	section := ""
	for line := range strings.SplitSeq(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if guest, ok := strings.CutPrefix(trimmed, "Virtual Machine: "); ok {
			if guest != name {
				return vms.Info{}, fmt.Errorf("vm info %s: answered for %q", name, guest)
			}
			found = true
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		key, value, isKey := strings.Cut(trimmed, ": ")
		value = dashless(strings.TrimSpace(value))

		switch {
		case indent <= 2 && !isKey:
			section = trimmed
			switch section {
			case "network-interface":
				info.Networks = append(info.Networks, vms.Network{})
			case "virtual-disk":
				info.Disks = append(info.Disks, vms.Disk{})
			}
		case indent <= 2:
			switch key {
			case "uuid":
				info.UUID = value
			case "memory-resident":
				info.MemoryResident = leadingInt64(value)
			}
		case section == "console-ports" && isKey:
			if info.Consoles == nil {
				info.Consoles = make(map[string]string)
			}
			info.Consoles[key] = value
		case section == "network-interface" && isKey:
			readNetworkKey(&info.Networks[len(info.Networks)-1], key, value)
		case section == "virtual-disk" && isKey:
			readDiskKey(&info.Disks[len(info.Disks)-1], key, value)
		}
	}
	if !found {
		return vms.Info{}, fmt.Errorf("vm info %s: no guest in the answer", name)
	}
	return info, nil
}

func readNetworkKey(n *vms.Network, key, value string) {
	switch key {
	case "number":
		n.Number, _ = strconv.Atoi(value)
	case "emulation":
		n.Emulation = value
	case "virtual-switch":
		n.Switch = value
	case "fixed-mac-address":
		n.MAC = value
	case "active-device":
		n.Device = value
	case "bridge":
		n.Bridge = value
	case "bytes-in":
		n.BytesIn = leadingInt64(value)
	case "bytes-out":
		n.BytesOut = leadingInt64(value)
	}
}

func readDiskKey(d *vms.Disk, key, value string) {
	switch key {
	case "number":
		d.Number, _ = strconv.Atoi(value)
	case "device-type":
		d.DeviceType = value
	case "emulation":
		d.Emulation = value
	case "system-path":
		d.Path = value
	case "bytes-size":
		d.Size = leadingInt64(value)
	case "bytes-used":
		d.Used = leadingInt64(value)
	}
}

// dashless is vm-bhyve's "-" for nothing, as nothing.
func dashless(value string) string {
	if value == "-" {
		return ""
	}
	return value
}

// leadingInt64 reads "41967616 (40.023M)" as 41967616: the bytes, without
// vm-bhyve's rounded restatement of them.
func leadingInt64(value string) *int64 {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

func atoiPtr(s string) *int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

// vmCommandError reports a vm-bhyve command that failed in a way that is not
// an answer, with vm-bhyve's own message where it gave one.
func vmCommandError(cmd system.Command, result system.CommandResult, err error) error {
	if problem := vmProblem(result.Stdout, result.Stderr); problem != "" {
		err = fmt.Errorf("%w: %s", err, problem)
	} else if msg := strings.TrimSpace(result.Stderr); msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	return &system.CommandError{Command: cmd.Name, Args: cmd.Args, Result: result, Err: err}
}

var _ vms.VMSystem = (*Adapter)(nil)
