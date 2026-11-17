package freebsd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/osversion"
	"github.com/ottermq/jaildeck/internal/system"
)

// script is the head of a real freebsd-version, as 15.1 ships it, down to the
// line that matters.
func script(version string) string {
	return "#!/bin/sh\n#-\n# SPDX-License-Identifier: BSD-2-Clause\n#\n\nset -e\n\n" +
		"USERLAND_VERSION=\"" + version + "\"\n\n: ${ROOT:=}\n"
}

// tree makes a FreeBSD-shaped root with a bin directory, and calls put to fill
// in bin/freebsd-version however the case wants it.
func tree(t *testing.T, put func(bin string)) string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if put != nil {
		put(bin)
	}
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestReadUserland(t *testing.T) {
	got, err := readUserland(tree(t, func(bin string) {
		write(t, filepath.Join(bin, "freebsd-version"), script("15.1-RELEASE-p2"))
	}))
	if err != nil || got != "15.1-RELEASE-p2" {
		t.Fatalf("readUserland = %q, %v; want 15.1-RELEASE-p2", got, err)
	}
}

// A symlink that stays inside the tree is followed: a jail is free to lay out
// its own files.
func TestReadUserlandFollowsASymlinkInside(t *testing.T) {
	root := tree(t, nil)
	other := filepath.Join(root, "usr", "bin")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(other, "freebsd-version"), script("15.1-RELEASE-p5"))
	if err := os.Symlink("../usr/bin/freebsd-version", filepath.Join(root, "bin", "freebsd-version")); err != nil {
		t.Fatal(err)
	}
	got, err := readUserland(root)
	if err != nil || got != "15.1-RELEASE-p5" {
		t.Fatalf("readUserland = %q, %v; want 15.1-RELEASE-p5", got, err)
	}
}

// The case JD-060 is about: root in a jail points the file, or the whole of
// /bin, at something on the host. Neither is followed.
func TestReadUserlandRefusesToLeaveTheTree(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "freebsd-version"), script("15.1-RELEASE-p6"))

	t.Run("the file", func(t *testing.T) {
		root := tree(t, func(bin string) {
			if err := os.Symlink(filepath.Join(outside, "freebsd-version"), filepath.Join(bin, "freebsd-version")); err != nil {
				t.Fatal(err)
			}
		})
		if got, err := readUserland(root); err == nil {
			t.Fatalf("readUserland = %q, nil; want an error for a symlink out of the tree", got)
		}
	})
	t.Run("the directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, "bin")); err != nil {
			t.Fatal(err)
		}
		if got, err := readUserland(root); err == nil {
			t.Fatalf("readUserland = %q, nil; want an error for a /bin that leaves the tree", got)
		}
	})
	// A jail's /bin/freebsd-version -> /usr/bin/freebsd-version means a file
	// inside the jail, but read from the host it names the host's own. os.Root
	// refuses every absolute link, which is the safe reading of it: the
	// version is then unknown.
	t.Run("an absolute link, even one meant inside", func(t *testing.T) {
		root := tree(t, nil)
		if err := os.MkdirAll(filepath.Join(root, "usr", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, "usr", "bin", "freebsd-version"), script("15.1-RELEASE-p5"))
		if err := os.Symlink("/usr/bin/freebsd-version", filepath.Join(root, "bin", "freebsd-version")); err != nil {
			t.Fatal(err)
		}
		if got, err := readUserland(root); err == nil {
			t.Fatalf("readUserland = %q, nil; want an absolute link refused", got)
		}
	})
	t.Run("a relative escape", func(t *testing.T) {
		root := tree(t, func(bin string) {
			rel, err := filepath.Rel(bin, filepath.Join(outside, "freebsd-version"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(rel, filepath.Join(bin, "freebsd-version")); err != nil {
				t.Fatal(err)
			}
		})
		if got, err := readUserland(root); err == nil {
			t.Fatalf("readUserland = %q, nil; want an error for ../ out of the tree", got)
		}
	})
}

// A FIFO would block an ordinary open until something wrote to it, holding a
// listing up for as long as the jail liked.
func TestReadUserlandRefusesAFIFO(t *testing.T) {
	root := tree(t, func(bin string) {
		if err := syscall.Mkfifo(filepath.Join(bin, "freebsd-version"), 0o644); err != nil {
			t.Skipf("mkfifo: %v", err)
		}
	})
	done := make(chan error, 1)
	go func() { _, err := readUserland(root); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotVersionScript) {
			t.Fatalf("readUserland on a FIFO = %v; want ErrNotVersionScript", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readUserland blocked on a FIFO")
	}
}

func TestReadUserlandRefusesWhatIsNotAVersion(t *testing.T) {
	for name, put := range map[string]func(t *testing.T, bin string){
		"missing": func(t *testing.T, bin string) {},
		"a directory": func(t *testing.T, bin string) {
			if err := os.Mkdir(filepath.Join(bin, "freebsd-version"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"no version line": func(t *testing.T, bin string) {
			write(t, filepath.Join(bin, "freebsd-version"), "#!/bin/sh\necho 15.1-RELEASE-p6\n")
		},
		"a command for a version": func(t *testing.T, bin string) {
			write(t, filepath.Join(bin, "freebsd-version"), script("$(id)"))
		},
		// The line past the read limit is not seen, so a padded file is
		// unknown rather than read in full.
		"the line past the limit": func(t *testing.T, bin string) {
			write(t, filepath.Join(bin, "freebsd-version"),
				strings.Repeat("#\n", versionScriptLimit)+script("15.1-RELEASE-p6"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := tree(t, func(bin string) { put(t, bin) })
			if got, err := readUserland(root); err == nil {
				t.Fatalf("readUserland = %q, nil; want an error", got)
			}
		})
	}
	if _, err := readUserland(""); err == nil {
		t.Error("readUserland(\"\") succeeded; a jail with no path has no userland to read")
	}
}

// stubRunner answers commands by their full line and fails the rest.
type stubRunner map[string]string

func (r stubRunner) Run(_ context.Context, cmd system.Command) (system.CommandResult, error) {
	if out, ok := r[cmd.String()]; ok {
		return system.CommandResult{Stdout: out}, nil
	}
	return system.CommandResult{ExitCode: 1, Stderr: "unexpected"}, errors.New("exit status 1")
}

func TestHostVersions(t *testing.T) {
	host := tree(t, func(bin string) {
		write(t, filepath.Join(bin, "freebsd-version"), script("15.1-RELEASE-p6"))
	})
	cases := []struct {
		name   string
		runner stubRunner
		want   osversion.Host
	}{
		{"updated, not restarted", stubRunner{
			"sysctl -n kern.osrelease": "15.1-RELEASE-p5\n",
			"freebsd-version -k":       "15.1-RELEASE-p6\n",
		}, osversion.Host{KernelRunning: "15.1-RELEASE-p5", KernelInstalled: "15.1-RELEASE-p6",
			Userland: "15.1-RELEASE-p6", RebootPending: true}},
		{"current", stubRunner{
			"sysctl -n kern.osrelease": "15.1-RELEASE-p6\n",
			"freebsd-version -k":       "15.1-RELEASE-p6\n",
		}, osversion.Host{KernelRunning: "15.1-RELEASE-p6", KernelInstalled: "15.1-RELEASE-p6",
			Userland: "15.1-RELEASE-p6"}},
		// freebsd-version -k exits 1 when it cannot find the kernel file.
		{"installed kernel not found", stubRunner{
			"sysctl -n kern.osrelease": "15.1-RELEASE-p6\n",
		}, osversion.Host{KernelRunning: "15.1-RELEASE-p6", Userland: "15.1-RELEASE-p6"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewAdapter(c.runner)
			a.hostRoot = host
			got, err := a.HostVersions(context.Background())
			if err != nil || got != c.want {
				t.Fatalf("HostVersions = %+v, %v; want %+v", got, err, c.want)
			}
		})
	}

	t.Run("nothing readable", func(t *testing.T) {
		a := NewAdapter(stubRunner{})
		a.hostRoot = t.TempDir()
		if got, err := a.HostVersions(context.Background()); err == nil {
			t.Fatalf("HostVersions = %+v, nil; want an error when neither the kernel nor the userland reads", got)
		}
	})
}
