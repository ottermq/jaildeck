package app

import (
	"context"
	"fmt"

	"github.com/ottermq/jaildeck/internal/metrics"
	"github.com/ottermq/jaildeck/internal/vms"
)

// processCPU is the host's process table, which the metrics adapters read.
type processCPU interface {
	ProcessCPU(ctx context.Context, pids []int) (map[int]float64, error)
}

// metricsGuests joins the vms domain's running guests with what their bhyve
// processes cost (#126): CPU from the process table, memory from `vm info`'s
// bhyvectl count, which is the one that grows with the guest.
type metricsGuests struct {
	vms *vms.Service
	cpu processCPU
}

func (m metricsGuests) GuestUsage(ctx context.Context) ([]metrics.GuestUsage, error) {
	listing, err := m.vms.List(ctx)
	if err != nil {
		return nil, err
	}
	if listing.VMBhyve != vms.VMBhyveReady {
		// No vm-bhyve is no guests, which the listing says on its own page.
		return nil, nil
	}
	var running []vms.VM
	pids := []int{}
	for _, vm := range listing.VMs {
		if vm.State == vms.StateRunning && vm.PID != nil {
			running = append(running, vm)
			pids = append(pids, *vm.PID)
		}
	}
	cpu, err := m.cpu.ProcessCPU(ctx, pids)
	if err != nil {
		return nil, fmt.Errorf("reading the guests' bhyve processes: %w", err)
	}
	out := make([]metrics.GuestUsage, 0, len(running))
	for _, vm := range running {
		usage := metrics.GuestUsage{Guest: vm.Name}
		percent, ok := cpu[*vm.PID]
		if !ok {
			usage.Error = "its bhyve process ended while it was being read"
			out = append(out, usage)
			continue
		}
		usage.PCPU = percent
		detail, err := m.vms.Get(ctx, vm.Name)
		switch {
		case err != nil:
			usage.Error = err.Error()
		case detail.MemoryResident == nil:
			usage.Error = "vm info gave no resident memory"
		default:
			usage.MemoryResident = *detail.MemoryResident
		}
		out = append(out, usage)
	}
	return out, nil
}
