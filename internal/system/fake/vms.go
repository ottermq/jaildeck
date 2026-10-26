package fake

import (
	"context"
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
}

func NewVMs() *VMs {
	return &VMs{
		running: map[string]bool{"jd-cap-running": true},
		pids:    map[string]int{"jd-cap-running": 16783},
		nextPID: 20000,
	}
}

func (f *VMs) VMList(context.Context) (vms.Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	guests := []vms.VM{
		{Name: "jd-cap-running", Datastore: "default", Loader: "uefi", CPU: "2", Memory: "512M",
			Autostart: true, AutostartOrder: 1},
		{Name: "jd-cap-stopped", Datastore: "default", Loader: "uefi", CPU: "1", Memory: "256M"},
	}
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

var _ vms.VMSystem = (*VMs)(nil)
