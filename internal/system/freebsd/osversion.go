package freebsd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/ottermq/jaildeck/internal/osversion"
	"github.com/ottermq/jaildeck/internal/system"
)

// versionScript is where a FreeBSD userland records its version, relative to
// its root. freebsd-update rewrites it whenever it updates the userland.
const versionScript = "bin/freebsd-version"

// versionScriptLimit is far more than the script is (about 4 KiB) and small
// enough that a jail which put something else there costs nothing to read.
const versionScriptLimit = 64 << 10

// ErrNotVersionScript is a freebsd-version that is not a regular file, or has
// no version line Jail Deck can trust.
var ErrNotVersionScript = errors.New("not a freebsd-version script")

// HostVersions reads the host's three versions (#229): the running kernel from
// kern.osrelease, the installed one from freebsd-version -k, and the userland
// from the host's own freebsd-version, read the same way a jail's is.
//
// A part that cannot be read is left empty rather than failing the rest; the
// installed kernel is the one most likely to be missing, on a host booted from
// something other than /boot/kernel.
func (a *Adapter) HostVersions(ctx context.Context) (osversion.Host, error) {
	running, runErr := a.Sysctl(ctx, "kern.osrelease")
	installed := ""
	// The host's own freebsd-version, root-owned and in the base system, so
	// running it is safe in a way that running a jail's never is (JD-060).
	result, err := a.runHost(ctx, system.Command{Name: "freebsd-version", Args: []string{"-k"}})
	if err == nil {
		installed = strings.TrimSpace(result.Stdout)
	}
	userland, userErr := a.Userland(ctx, a.hostRoot)
	if runErr != nil && userErr != nil {
		return osversion.Host{}, fmt.Errorf("reading the host's versions: %w", errors.Join(runErr, userErr))
	}
	return osversion.NewHost(running, installed, userland), nil
}

// Userland reads the userland version of the FreeBSD tree at root: a jail's
// path, a template's mountpoint, or / for the host (#229, JD-060).
//
// The script is read as text and never run. A jail's copy belongs to whoever
// is root in that jail, and the root process running it would hand them the
// host. os.Root keeps the path inside root, so a /bin that is a symlink out to
// the host is refused rather than followed. The file is opened non-blocking
// and must be a regular file, so a FIFO or a device in its place cannot hang
// or flood the read, and only its USERLAND_VERSION line is used, and only
// when it is shaped like a version.
func (a *Adapter) Userland(ctx context.Context, root string) (string, error) {
	return readUserland(root)
}

func readUserland(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("%w: no root", ErrNotVersionScript)
	}
	tree, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer tree.Close()
	f, err := tree.OpenFile(versionScript, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s in %s is %s", ErrNotVersionScript, versionScript, root, info.Mode().Type())
	}
	content, err := io.ReadAll(io.LimitReader(f, versionScriptLimit))
	if err != nil {
		return "", err
	}
	version, ok := osversion.UserlandLine(content)
	if !ok {
		return "", fmt.Errorf("%w: no version line in %s in %s", ErrNotVersionScript, versionScript, root)
	}
	return version, nil
}
