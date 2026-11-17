package freebsd

import (
	"context"
	"errors"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
)

func TestResidentMemory(t *testing.T) {
	// ps reports kilobytes, right-aligned.
	r := &auditRunner{result: system.CommandResult{Stdout: "13244  22328\n13248  85328\n"}}
	got, err := auditAdapter(r).ResidentMemory(context.Background(), []int{13244, 13248})
	if err != nil {
		t.Fatal(err)
	}
	if got[13244] != 22328*1024 || got[13248] != 85328*1024 {
		t.Fatalf("got %v", got)
	}
	// Separate -o flags: FreeBSD's ps reads `pid=,rss=` as one column with
	// the header ",rss=".
	if cmd := r.ran[0].String(); cmd != "ps -o pid= -o rss= -p 13244,13248" {
		t.Errorf("ran %q", cmd)
	}
}

// The front between a crash and its restart: ps prints the others and exits
// 1. That is an answer, not a failure.
func TestResidentMemoryOneGone(t *testing.T) {
	r := &auditRunner{result: system.CommandResult{ExitCode: 1, Stdout: "13244  22328\n"}, err: errors.New("exit status 1")}
	got, err := auditAdapter(r).ResidentMemory(context.Background(), []int{13244, 99999})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	r = &auditRunner{result: system.CommandResult{ExitCode: -1}, err: errors.New("ps: not found")}
	if _, err := auditAdapter(r).ResidentMemory(context.Background(), []int{1}); err == nil {
		t.Fatal("a ps that could not run read as an answer")
	}
}
