package fake

import (
	"context"

	"github.com/ottermq/jaildeck/internal/vms"
)

// VMs is an in-memory vm-bhyve with the two guests capture-vm-guests-induced.sh
// made on isengard: one running on a switch of its own and started at boot,
// one stopped with no network.
//
// Ready, because a fake that showed vm-bhyve not installed -- the honest
// default for most hosts -- would leave the rest of the page unbuildable.
type VMs struct{}

func NewVMs() *VMs { return &VMs{} }

func (f *VMs) VMList(context.Context) (vms.Listing, error) {
	pid := 16783
	return vms.Listing{VMBhyve: vms.VMBhyveReady, VMs: []vms.VM{
		{Name: "jd-cap-running", Datastore: "default", Loader: "uefi", CPU: "2", Memory: "512M",
			State: vms.StateRunning, PID: &pid, Autostart: true, AutostartOrder: 1},
		{Name: "jd-cap-stopped", Datastore: "default", Loader: "uefi", CPU: "1", Memory: "256M",
			State: vms.StateStopped},
	}}, nil
}

func (f *VMs) VMInfo(_ context.Context, name string) (vms.Info, error) {
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
