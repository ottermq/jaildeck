package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/config"
)

// The state file becomes the front's uid's to read, in a directory nobody
// can list (JD-056).
func TestShareStateDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		t.Skip("chown to uid 0 needs root")
	}
	if err := shareStateDir(dir, 65534); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o711, filepath.Join(dir, stateFileName): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: %#o, want %#o", path, got, want)
		}
	}
}

// A state dir with anything else in it is not opened to a group.
func TestShareStateDirRefusesASharedDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{stateFileName, "something-else"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err := shareStateDir(dir, os.Getuid())
	if err == nil || !strings.Contains(err.Error(), "something-else") {
		t.Fatalf("err = %v, want a refusal naming the other file", err)
	}
}

func TestPrepareRunDirRefusesAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareRunDir(path); err == nil {
		t.Fatal("a file was taken for the run directory")
	}
}

// Not root, or JAILDECK_SPLIT=off, is one process: a development box, the
// tests, and a host that opts out.
func TestSplitUserIsNilUnlessRoot(t *testing.T) {
	for _, off := range []bool{false, true} {
		account, err := splitUser(&config.Config{User: "jaildeck", OneProcess: off})
		if (off || os.Geteuid() != 0) && (account != nil || err != nil) {
			t.Errorf("OneProcess=%v: %v, %v", off, account, err)
		}
	}
}

// The root side's socket takes connections from the front's uid and root
// alone, by the kernel's account, whatever the socket's mode.
func TestOnlyFromDropsAnotherUID(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root is always let in")
	}
	for _, c := range []struct {
		uid  int
		want bool
	}{{os.Getuid(), true}, {os.Getuid() + 12345, false}} {
		path := filepath.Join(t.TempDir(), "s")
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		l = onlyFrom(l, c.uid)
		accepted := make(chan bool, 1)
		go func() {
			conn, err := l.Accept()
			if err == nil {
				conn.Close()
			}
			accepted <- err == nil
		}()
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		var got bool
		select {
		case got = <-accepted:
		case <-time.After(500 * time.Millisecond):
		}
		conn.Close()
		l.Close()
		if got != c.want {
			t.Errorf("uid %d: accepted %v, want %v", c.uid, got, c.want)
		}
	}
}

func TestTheFrontRefusesToStartByHand(t *testing.T) {
	t.Setenv(frontListenersEnv, "")
	if err := runFront(&config.Config{}); err == nil {
		t.Fatal("the front started without its listeners")
	}
}
