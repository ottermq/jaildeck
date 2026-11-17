package app

import (
	"context"
	"errors"
	"os"
	"runtime/metrics"
	"sync/atomic"

	jdmetrics "github.com/ottermq/jaildeck/internal/metrics"
)

// residentReader reads processes' resident memory by pid (#224).
type residentReader interface {
	ResidentMemory(ctx context.Context, pids []int) (map[int]int64, error)
}

// ownProcesses is Jail Deck's own memory: this process, and the front when
// this is the root side of a split Jail Deck (JD-056).
type ownProcesses struct {
	rss residentReader
	// front is the front's pid, 0 while it is not running. Nil when Jail
	// Deck runs as one process, which has no front of its own.
	front atomic.Pointer[func() int]
}

func (o *ownProcesses) Processes(ctx context.Context) ([]jdmetrics.Process, error) {
	self := os.Getpid()
	pids := []int{self}
	side, frontPID := "single", 0
	if front := o.front.Load(); front != nil {
		side = "back"
		if frontPID = (*front)(); frontPID > 0 {
			pids = append(pids, frontPID)
		}
	}
	rss, err := o.rss.ResidentMemory(ctx, pids)
	if err != nil {
		return nil, err
	}
	resident, ok := rss[self]
	if !ok {
		return nil, errors.New("ps did not report Jail Deck's own process")
	}
	heap := heapInuse()
	out := []jdmetrics.Process{{Side: side, Resident: resident, HeapInuse: &heap}}
	if frontResident, ok := rss[frontPID]; frontPID > 0 && ok {
		out = append(out, jdmetrics.Process{Side: "front", Resident: frontResident})
	}
	return out, nil
}

// heapInuse is runtime.MemStats.HeapInuse read through runtime/metrics, which
// does not stop the world to answer: the heap's live objects plus its
// unused, still-held spans.
func heapInuse() int64 {
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/unused:bytes"},
	}
	metrics.Read(samples)
	var total uint64
	for _, s := range samples {
		if s.Value.Kind() == metrics.KindUint64 {
			total += s.Value.Uint64()
		}
	}
	return int64(total)
}
