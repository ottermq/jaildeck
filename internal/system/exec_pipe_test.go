package system

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPipeFeedsTheSecondCommand(t *testing.T) {
	got, err := NewExecCommandRunner().Run(context.Background(), Command{
		Name: "printf", Args: []string{"a\nb\nc\n"},
		Into: &Command{Name: "wc", Args: []string{"-l"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got.Stdout) != "3" {
		t.Errorf("stdout %q, want 3", got.Stdout)
	}
}

// A writer that fails is the cause, even though the reader then fails on a
// short stream as well.
func TestPipeReportsTheWriterFirst(t *testing.T) {
	_, err := NewExecCommandRunner().Run(context.Background(), Command{
		Name: "false", Into: &Command{Name: "cat"},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "false: ") {
		t.Fatalf("got %v", err)
	}
}

// A reader that fails first takes the writer down with a broken pipe; the
// reader is still the cause. A long stream, so the writer is still writing
// when the reader goes.
func TestPipeBlamesTheReaderForABrokenPipe(t *testing.T) {
	_, err := NewExecCommandRunner().Run(context.Background(), Command{
		Name: "yes", Into: &Command{Name: "ls", Args: []string{"/no/such/place"}},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "ls /no/such/place: ") {
		t.Fatalf("got %v", err)
	}
}

func TestPipeReportsAFailingReader(t *testing.T) {
	got, err := NewExecCommandRunner().Run(context.Background(), Command{
		Name: "printf", Args: []string{"x"},
		Into: &Command{Name: "ls", Args: []string{"/no/such/place"}},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "ls /no/such/place: ") || got.ExitCode == 0 {
		t.Fatalf("got %v, exit %d", err, got.ExitCode)
	}
	if !strings.HasPrefix(got.Stderr, "ls /no/such/place: ") {
		t.Errorf("stderr not labelled: %q", got.Stderr)
	}
}

func TestPipeNamesAMissingCommand(t *testing.T) {
	_, err := NewExecCommandRunner().Run(context.Background(), Command{
		Name: "printf", Args: []string{"x"}, Into: &Command{Name: "no-such-command-258"},
	})
	if !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestPipeTimesOutBothHalves(t *testing.T) {
	start := time.Now()
	_, err := NewExecCommandRunner().Run(context.Background(), Command{
		Name: "sleep", Args: []string{"30"}, Timeout: 200 * time.Millisecond,
		Into: &Command{Name: "cat"},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestPipeRefusesWhatItCannotDo(t *testing.T) {
	for _, cmd := range []Command{
		{Name: "true", Detach: true, Into: &Command{Name: "cat"}},
		{Name: "true", StdoutFile: "/dev/null", Into: &Command{Name: "cat"}},
		{Name: "true", Into: &Command{Name: "cat", Into: &Command{Name: "cat"}}},
	} {
		if _, err := NewExecCommandRunner().Run(context.Background(), cmd); !errors.Is(err, ErrBadPipe) {
			t.Errorf("%s: %v", cmd, err)
		}
	}
}

func TestPipeIsWrittenAsOne(t *testing.T) {
	cmd := Command{Name: "zfs", Args: []string{"send", "a@base"},
		Into: &Command{Name: "zfs", Args: []string{"receive", "b@copy"}}}
	if got := cmd.String(); got != "zfs send a@base | zfs receive b@copy" {
		t.Errorf("got %q", got)
	}
}
