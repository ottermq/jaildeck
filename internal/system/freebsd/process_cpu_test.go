package freebsd

import (
	"context"
	"errors"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
)

func TestProcessCPUReadsEveryPidInOneCall(t *testing.T) {
	calls := 0
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		calls++
		if cmd.String() != "ps -o pid= -o pcpu= -p 44210,44388,9" {
			t.Fatalf("command = %q", cmd.String())
		}
		// 9 ended between the listing and this read.
		return system.CommandResult{Stdout: "44210 195.5\n44388   0.0\n"}, nil
	}))
	got, err := a.ProcessCPU(context.Background(), []int{44210, 44388, 9})
	if err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	if len(got) != 2 || got[44210] != 195.5 || got[44388] != 0 {
		t.Errorf("cpu = %v", got)
	}
}

func TestProcessCPUWithNoneLeftIsEmpty(t *testing.T) {
	a := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{ExitCode: 1}, errors.New("exit status 1")
	}))
	got, err := a.ProcessCPU(context.Background(), []int{9})
	if err != nil || len(got) != 0 {
		t.Errorf("cpu = %v, %v", got, err)
	}
	if got, err := NewAdapter(nil).ProcessCPU(context.Background(), nil); err != nil || len(got) != 0 {
		t.Errorf("no pids: %v, %v", got, err)
	}
}
