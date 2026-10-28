package vms

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// OperationPinCPUs is the audit log's name for writing a guest's pin (JD-041).
const OperationPinCPUs = "vm-pin-cpus"

// PinOptions is a cpuset as bhyve_options: bhyve's -p <vcpu>:<hostcpu> for
// each vCPU, in order. vm-bhyve has no setting of its own for pinning and
// passes bhyve_options to bhyve as they are (JD-041).
func PinOptions(cpus []int) string {
	options := make([]string, len(cpus))
	for vcpu, cpu := range cpus {
		options[vcpu] = fmt.Sprintf("-p %d:%d", vcpu, cpu)
	}
	return strings.Join(options, " ")
}

// parsePins reads bhyve_options back into a cpuset. It is a cpuset only when
// the options are exactly what PinOptions writes -- every vCPU from 0, in
// order, and nothing else -- so that options someone else wrote are never
// mistaken for one.
func parsePins(options string) ([]int, bool) {
	fields := strings.Fields(options)
	if len(fields) == 0 || len(fields)%2 != 0 {
		return nil, false
	}
	cpus := make([]int, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		vcpu, cpu, found := strings.Cut(fields[i+1], ":")
		if fields[i] != "-p" || !found || vcpu != strconv.Itoa(len(cpus)) {
			return nil, false
		}
		n, err := strconv.Atoi(cpu)
		if err != nil || n < 0 || strconv.Itoa(n) != cpu {
			return nil, false
		}
		cpus = append(cpus, n)
	}
	return cpus, true
}

// checkCPUSet refuses a cpuset that does not pin every vCPU to its own host
// CPU. A partial pin is something bhyve takes (measured: the other vCPU
// roams), but a guest half pinned is not what anyone asking for a cpuset
// meant. hostCPUs is checked separately, once the host has been read.
func checkCPUSet(cpus []int, vcpus int) error {
	bad := func(format string, args ...any) error {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(format, args...))
	}
	if len(cpus) != vcpus {
		return bad("cpuset names %d host CPUs for %d vCPUs; give one for each vCPU, in order", len(cpus), vcpus)
	}
	for i, cpu := range cpus {
		if cpu < 0 {
			return bad("cpuset[%d] is %d; host CPUs are numbered from 0", i, cpu)
		}
		if slices.Index(cpus, cpu) != i {
			return bad("cpuset names host CPU %d twice; each vCPU gets one of its own", cpu)
		}
	}
	return nil
}

// checkHostCPUs refuses a pin to a CPU the host does not have, which bhyve
// would only find at the guest's start, by aborting -- and vm start still
// exits 0 (JD-041, measured).
func checkHostCPUs(cpus []int, hostCPUs int) error {
	for i, cpu := range cpus {
		if cpu >= hostCPUs {
			return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
				"cpuset[%d] is %d and this host has CPUs 0 to %d", i, cpu, hostCPUs-1))
		}
	}
	return nil
}
