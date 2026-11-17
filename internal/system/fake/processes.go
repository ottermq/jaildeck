package fake

import "context"

// Processes answers every pid with a resident size like the ones measured on
// a FreeBSD host: about 22 MB for the root side and 85 MB for the front, which
// holds the embedded interface (#224).
type Processes struct{}

func NewProcesses() Processes { return Processes{} }

func (Processes) ResidentMemory(_ context.Context, pids []int) (map[int]int64, error) {
	out := map[int]int64{}
	for i, pid := range pids {
		out[pid] = []int64{22 << 20, 85 << 20}[min(i, 1)]
	}
	return out, nil
}
