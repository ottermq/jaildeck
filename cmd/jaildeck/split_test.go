package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/config"
)

// The state dir becomes the front's to read, and nothing more (JD-056).
// The owner stays as it was here: a test is not root, so it chowns to the
// owner it already has, in its own group.
func TestShareStateDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		t.Skip("chown to uid 0 needs root")
	}
	if err := shareStateDir(dir, os.Getgid()); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o750, filepath.Join(dir, stateFileName): 0o640} {
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
	err := shareStateDir(dir, os.Getgid())
	if err == nil || !strings.Contains(err.Error(), "something-else") {
		t.Fatalf("err = %v, want a refusal naming the other file", err)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm()&0o070 == 0o050 {
		t.Error("the directory was opened to its group anyway")
	}
}

func TestPrepareRunDirRefusesAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareRunDir(path, os.Getgid()); err == nil {
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

func TestTheFrontRefusesToStartByHand(t *testing.T) {
	t.Setenv(frontListenersEnv, "")
	if err := runFront(&config.Config{}); err == nil {
		t.Fatal("the front started without its listeners")
	}
}
