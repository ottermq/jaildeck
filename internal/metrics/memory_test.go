package metrics

import (
	"strings"
	"testing"
)

func int64p(v int64) *int64 { return &v }

// Held leaves out what is given back on demand: free, inactive and the ARC
// (#224).
func TestHeldBytes(t *testing.T) {
	const gib = int64(1) << 30
	cases := []struct {
		name string
		m    Memory
		want int64
	}{
		// About isengard's shape a day after a reboot: most of "used" is
		// inactive, and the ARC is small for now.
		{"inactive and ARC left out", Memory{Physical: 128 * gib, Free: 101 * gib, Inactive: 14 * gib, ARC: int64p(5 * gib)}, 8 * gib},
		{"no ZFS", Memory{Physical: 16 * gib, Free: 4 * gib, Inactive: 2 * gib}, 10 * gib},
		// The ARC is part of wired, and counters are read one at a time, so
		// the arithmetic can overshoot: never below zero.
		{"never negative", Memory{Physical: 16 * gib, Free: 10 * gib, Inactive: 4 * gib, ARC: int64p(3 * gib)}, 0},
	}
	for _, tc := range cases {
		if got := tc.m.HeldBytes(); got != tc.want {
			t.Errorf("%s: held %d GiB, want %d GiB", tc.name, got/gib, tc.want/gib)
		}
	}
}

func TestMemoryMetrics(t *testing.T) {
	report := aReport()
	report.Host.Memory.Laundry = 4096
	report.Host.Memory.ARC = int64p(2952790016)
	report.Host.Memory.ARCMax = int64p(16046022656)
	report.Host.Memory.Held = report.Host.Memory.HeldBytes()
	body := render(t, report)
	checkFormat(t, body)
	requireLine(t, body, `jaildeck_host_memory_bytes{state="laundry"} 4096`)
	requireLine(t, body, `jaildeck_host_memory_bytes{state="arc"} 2.952790016e+09`)
	requireLine(t, body, "jaildeck_host_arc_max_bytes 1.6046022656e+10")
	if !strings.Contains(body, "jaildeck_host_memory_held_bytes ") {
		t.Error("no jaildeck_host_memory_held_bytes")
	}
	// Held is derived, so it is not one of the states a sum adds up.
	if strings.Contains(body, `state="held"`) {
		t.Error("held written as a memory state")
	}
}

// A host without ZFS has no ARC series at all, not a 0.
func TestMemoryMetricsWithoutZFS(t *testing.T) {
	body := render(t, aReport())
	checkFormat(t, body)
	for _, absent := range []string{`state="arc"`, "jaildeck_host_arc_max_bytes"} {
		if strings.Contains(body, absent) {
			t.Errorf("%s written on a host without ZFS", absent)
		}
	}
}

func TestProcessMetrics(t *testing.T) {
	report := aReport()
	report.Processes = []Process{
		{Side: "back", Resident: 22 << 20, HeapInuse: int64p(9 << 20)},
		{Side: "front", Resident: 85 << 20},
	}
	body := render(t, report)
	checkFormat(t, body)
	requireLine(t, body, `jaildeck_process_resident_memory_bytes{side="back"} 2.3068672e+07`)
	requireLine(t, body, `jaildeck_process_resident_memory_bytes{side="front"} 8.912896e+07`)
	requireLine(t, body, `jaildeck_go_heap_inuse_bytes{side="back"} 9.437184e+06`)
	if strings.Contains(body, `jaildeck_go_heap_inuse_bytes{side="front"}`) {
		t.Error("a heap written for the front, which the root side cannot read")
	}
}
