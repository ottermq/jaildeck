package app

import (
	"context"
	"os"
	"testing"
)

type stubResidents map[int]int64

func (s stubResidents) ResidentMemory(_ context.Context, pids []int) (map[int]int64, error) {
	out := map[int]int64{}
	for _, pid := range pids {
		if v, ok := s[pid]; ok {
			out[pid] = v
		}
	}
	return out, nil
}

func TestOwnProcessesSingle(t *testing.T) {
	o := &ownProcesses{rss: stubResidents{os.Getpid(): 30 << 20}}
	got, err := o.Processes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Side != "single" || got[0].Resident != 30<<20 || got[0].HeapInuse == nil || *got[0].HeapInuse <= 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestOwnProcessesSplit(t *testing.T) {
	front := 4242
	o := &ownProcesses{rss: stubResidents{os.Getpid(): 22 << 20, front: 85 << 20}}
	pid := func() int { return front }
	o.front.Store(&pid)
	got, err := o.Processes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Side != "back" || got[1].Side != "front" || got[1].Resident != 85<<20 || got[1].HeapInuse != nil {
		t.Fatalf("got %+v", got)
	}

	// Between a crash and its restart there is no front: the root side
	// still reports itself.
	front = 0
	got, err = o.Processes(context.Background())
	if err != nil || len(got) != 1 || got[0].Side != "back" {
		t.Fatalf("with no front: %+v, %v", got, err)
	}
}

func TestOwnProcessesSelfMissing(t *testing.T) {
	o := &ownProcesses{rss: stubResidents{}}
	if _, err := o.Processes(context.Background()); err == nil {
		t.Fatal("no error when ps did not report this process")
	}
}
