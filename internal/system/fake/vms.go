package fake

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/ottermq/jaildeck/internal/vms"
)

// VMs is an in-memory vm-bhyve with the two guests capture-vm-guests-induced.sh
// made on isengard: one running on a switch of its own and started at boot,
// one stopped with no network.
//
// Ready, because a fake that showed vm-bhyve not installed -- the honest
// default for most hosts -- would leave the rest of the page unbuildable.
//
// Start and stop change what it lists, at once and always cleanly: the
// timing and the guest that ignores the power button are the service tests'
// to exercise, not the fake's.
type VMs struct {
	mu      sync.Mutex
	running map[string]bool
	nextPID int
	pids    map[string]int
	// made are guests created here, and gone the seeded ones destroyed.
	made map[string]*madeVM
	gone map[string]bool
	// switches, when set, is where the switches come from: the fake network
	// host, so a switch it creates is one a new guest can be put on.
	switches *Networks
}

// SwitchesFrom makes the switches the fake network host's, as they are one
// thing on a real host.
func (f *VMs) SwitchesFrom(n *Networks) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switches = n
}

// madeVM is what the fake keeps of a guest it created.
type madeVM struct {
	spec   vms.CreateSpec
	sw     string
	disk   int64
	origin string
}

func NewVMs() *VMs {
	return &VMs{
		running: map[string]bool{"jd-cap-running": true},
		pids:    map[string]int{"jd-cap-running": 16783},
		nextPID: 20000,
		made:    map[string]*madeVM{},
		gone:    map[string]bool{},
	}
}

func (f *VMs) VMList(context.Context) (vms.Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seeded := []vms.VM{
		{Name: "jd-cap-running", Datastore: "default", Loader: "uefi", CPU: "2", Memory: "512M",
			Autostart: true, AutostartOrder: 1},
		{Name: "jd-cap-stopped", Datastore: "default", Loader: "uefi", CPU: "1", Memory: "256M"},
	}
	guests := make([]vms.VM, 0, len(seeded)+len(f.made))
	for _, vm := range seeded {
		if !f.gone[vm.Name] {
			guests = append(guests, vm)
		}
	}
	for name, m := range f.made {
		guests = append(guests, vms.VM{Name: name, Datastore: "default", Loader: "uefi",
			CPU: strconv.Itoa(m.spec.CPU), Memory: m.spec.Memory})
	}
	slices.SortFunc(guests, func(a, b vms.VM) int { return strings.Compare(a.Name, b.Name) })
	for i := range guests {
		guests[i].State = vms.StateStopped
		if f.running[guests[i].Name] {
			pid := f.pids[guests[i].Name]
			guests[i].State, guests[i].PID = vms.StateRunning, &pid
		}
	}
	return vms.Listing{VMBhyve: vms.VMBhyveReady, VMs: guests}, nil
}

func (f *VMs) VMStart(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextPID++
	f.running[name], f.pids[name] = true, f.nextPID
	return "vm start " + name, nil
}

func (f *VMs) VMStop(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, name)
	return "vm stop " + name, nil
}

func (f *VMs) VMPoweroff(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, name)
	return "vm poweroff -f " + name, nil
}

func (f *VMs) VMInfo(ctx context.Context, name string) (vms.Info, error) {
	info, err := f.info(name)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil && !f.running[name] {
		// What a guest has only while its bhyve instance exists.
		info.MemoryResident, info.Consoles = nil, nil
		for i := range info.Networks {
			info.Networks[i].Device, info.Networks[i].Bridge = "", ""
			info.Networks[i].BytesIn, info.Networks[i].BytesOut = nil, nil
		}
	}
	return info, err
}

func (f *VMs) info(name string) (vms.Info, error) {
	size, used := int64(1073741824), int64(57344)
	f.mu.Lock()
	m, made := f.made[name]
	gone := f.gone[name]
	f.mu.Unlock()
	if gone {
		return vms.Info{}, vms.ErrNoSuchVM
	}
	if made {
		info := vms.Info{UUID: "c12b2ce4-d17f-11f1-b463-e80688cecc46", Networks: []vms.Network{},
			Disks: []vms.Disk{{Number: 0, DeviceType: "sparse-zvol", Emulation: "virtio-blk",
				Path: "/dev/zvol/zroot/vm/" + name + "/disk0", Size: &m.disk, Used: &used}}}
		if m.sw != "" {
			info.Networks = append(info.Networks, vms.Network{Number: 0, Emulation: "virtio-net",
				Switch: m.sw, MAC: "58:9c:fc:05:52:a0", Device: "tap1", Bridge: "vm-" + m.sw})
		}
		return info, nil
	}
	switch name {
	case "jd-cap-running":
		resident, in, out := int64(41967616), int64(0), int64(553)
		return vms.Info{
			UUID:           "ed117915-d146-11f1-b463-e80688cecc46",
			MemoryResident: &resident,
			Consoles:       map[string]string{"com1": "/dev/nmdm-jd-cap-running.1B"},
			Networks: []vms.Network{{Number: 0, Emulation: "virtio-net", Switch: "jd-capsw",
				MAC: "58:9c:fc:02:fa:82", Device: "tap0", Bridge: "vm-jd-capsw",
				BytesIn: &in, BytesOut: &out}},
			Disks: []vms.Disk{{Number: 0, DeviceType: "sparse-zvol", Emulation: "virtio-blk",
				Path: "/dev/zvol/tank/vm/jd-cap-running/disk0", Size: &size, Used: &used}},
		}, nil
	case "jd-cap-stopped":
		return vms.Info{
			UUID:     "eba5d268-d146-11f1-b463-e80688cecc46",
			Networks: []vms.Network{},
			Disks: []vms.Disk{{Number: 0, DeviceType: "sparse-zvol", Emulation: "virtio-blk",
				Path: "/dev/zvol/tank/vm/jd-cap-stopped/disk0", Size: &size, Used: &used}},
		}, nil
	}
	return vms.Info{}, vms.ErrNoSuchVM
}

// VMSwitches is the one switch the seeded running guest is on, or the fake
// network host's when the two are joined.
func (f *VMs) VMSwitches(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	n := f.switches
	f.mu.Unlock()
	if n == nil {
		return []string{"jd-capsw"}, nil
	}
	switches, _ := n.Switches(ctx)
	names := make([]string, 0, len(switches))
	for _, s := range switches {
		names = append(names, s.Name)
	}
	return names, nil
}

func (f *VMs) VMCreate(_ context.Context, spec vms.CreateSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.made[spec.Name] = &madeVM{spec: spec}
	delete(f.gone, spec.Name)
	return "vm create -t jaildeck -c " + strconv.Itoa(spec.CPU) + " -m " + spec.Memory +
		" -s " + spec.Disk + " -C " + spec.Name, nil
}

func (f *VMs) VMAddNetwork(_ context.Context, name, vmSwitch string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.made[name]; ok {
		m.sw = vmSwitch
	}
	return "vm add -d network -s " + vmSwitch + " " + name, nil
}

func (f *VMs) VMReplaceDisk(_ context.Context, name, snapshot string, size int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.made[name]; ok {
		m.disk, m.origin = size, snapshot
	}
	disk := "zroot/vm/" + name + "/disk0"
	return "zfs destroy " + disk + " && zfs clone -o volmode=dev -o volsize=" +
		strconv.FormatInt(size, 10) + " " + snapshot + " " + disk, nil
}

func (f *VMs) VMDestroy(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.made[name]; ok {
		delete(f.made, name)
	} else {
		f.gone[name] = true
	}
	return "vm destroy -f " + name, nil
}

var _ vms.VMSystem = (*VMs)(nil)
