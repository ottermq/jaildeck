package freebsd

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/templates"
)

// mirror is where releases come from. download.freebsd.org is the project's
// own redirector to a nearby mirror, so there is nothing to configure and
// nothing to keep up to date.
const mirror = "https://download.freebsd.org/releases"

// mediaDir is where fetched archives live, beside templates and containers
// under the jails dataset (JD-006, JD-021). A path rather than a dataset name:
// fetch(1) writes files, not datasets.
const mediaDir = "/usr/local/jails/media"

// fetchTimeout bounds the download. Every command runs under a deadline
// (JD-014); this one is simply much larger than jls gets, because 372 MB over
// a slow link is a legitimate half hour and killing it at ten seconds would be
// Jail Deck giving up on a working system.
const fetchTimeout = 45 * time.Minute

// ReleaseAdapter prepares release templates from the FreeBSD mirror. It
// implements templates.ReleaseSource (JD-010).
type ReleaseAdapter struct {
	*Adapter
	// media is overridden in tests; a host never changes it.
	media string
}

func NewReleaseAdapter(runner system.CommandRunner) *ReleaseAdapter {
	return &ReleaseAdapter{Adapter: NewAdapter(runner), media: mediaDir}
}

// Host reports the release and architecture to build for.
//
// The release is normalised to drop the patch level: freebsd-version and
// kern.osrelease both say 15.1-RELEASE-p5, and the mirror publishes no -pN
// directory at all — .../15.1-RELEASE-p5/base.txz is a 404 while
// .../15.1-RELEASE/base.txz is not. The patch level is what freebsd-update
// brings the extracted userland to afterwards, not what is downloaded.
func (a *ReleaseAdapter) Host(ctx context.Context) (templates.Host, error) {
	release, err := a.Sysctl(ctx, "kern.osrelease")
	if err != nil {
		return templates.Host{}, err
	}
	arch, err := a.Sysctl(ctx, "hw.machine")
	if err != nil {
		return templates.Host{}, err
	}
	return templates.Host{Release: releaseOf(release), Arch: strings.TrimSpace(arch)}, nil
}

// releaseOf drops a trailing -pN. Anything else is passed through untouched,
// including -STABLE and -CURRENT, which have no patch level to drop.
func releaseOf(version string) string {
	version = strings.TrimSpace(version)
	cut := strings.LastIndex(version, "-p")
	if cut < 0 {
		return version
	}
	if digits := version[cut+2:]; digits != "" && strings.Trim(digits, "0123456789") == "" {
		return version[:cut]
	}
	return version
}

// Fetched lists the releases whose base.txz is already in media/.
//
// One `ls` of a directory of directories: the state of a template that has
// been fetched and not yet extracted is a file on the disk, which is what
// JD-008 reads it from. A missing media directory is an answer, not an error —
// a host where nothing has been fetched yet.
func (a *ReleaseAdapter) Fetched(ctx context.Context) ([]string, error) {
	result, err := a.runner.Run(ctx, system.Command{
		Name: "sh",
		Args: []string{"-c", fmt.Sprintf(
			"for d in %s/*/; do [ -f \"$d/base.txz\" ] && basename \"$d\"; done", a.media)},
	})
	if err != nil {
		// The loop exits non-zero when the last iteration's test fails, and
		// with nothing at all when the glob matches nothing. Neither is a
		// failure to read the directory; both mean "none".
		if result.ExitCode > 0 && strings.TrimSpace(result.Stderr) == "" {
			return nil, nil
		}
		return nil, err
	}
	return nonEmptyLines(result.Stdout), nil
}

// FetchBase downloads a release's base.txz into media/ and verifies it against
// the release's own MANIFEST before reporting success.
//
// Three commands, each audited by the caller through the one Result: fetch the
// MANIFEST, fetch the archive, checksum the archive. The verification is not
// optional politeness — an archive that arrived corrupt would be extracted into
// a template every later jail is cloned from.
func (a *ReleaseAdapter) FetchBase(ctx context.Context, release, arch string) (templates.Result, error) {
	base := fmt.Sprintf("%s/%s/%s", mirror, arch, release)
	dir := path.Join(a.media, release)
	archive := path.Join(dir, "base.txz")

	result := templates.Result{Root: archive, Command: "fetch -o " + archive + " " + base + "/base.txz"}

	if _, err := a.runHost(ctx, system.Command{
		Name: "mkdir", Args: []string{"-p", dir},
	}); err != nil {
		return result, err
	}

	manifest, err := a.runHost(ctx, system.Command{
		Name:    "fetch",
		Args:    []string{"-q", "-o", "-", base + "/MANIFEST"},
		Timeout: time.Minute,
	})
	if err != nil {
		return result, err
	}
	want, err := manifestSHA256(manifest.Stdout, "base.txz")
	if err != nil {
		return result, err
	}

	// -a resumes a partial file rather than starting again, which is what
	// makes repeating the call after a timeout cheap (JD-016).
	if _, err := a.runHost(ctx, system.Command{
		Name:    "fetch",
		Args:    []string{"-a", "-o", archive, base + "/base.txz"},
		Timeout: fetchTimeout,
	}); err != nil {
		return result, err
	}

	sum, err := a.runHost(ctx, system.Command{
		Name:    "sha256",
		Args:    []string{"-q", archive},
		Timeout: 5 * time.Minute,
	})
	if err != nil {
		return result, err
	}
	if got := strings.TrimSpace(sum.Stdout); got != want {
		return result, common.NewAppError(http.StatusBadGateway, fmt.Sprintf(
			"%s does not match the checksum its own MANIFEST publishes "+
				"(got %s, want %s); the download is corrupt and was not kept", archive, got, want))
	}
	return result, nil
}

// extractTimeout bounds unpacking. Smaller than the fetch's because this is
// local I/O rather than a download over whatever link the host has, and still
// far larger than anything else (JD-014).
const extractTimeout = 30 * time.Minute

// Extract unpacks a fetched release into root and leaves the userland able to
// resolve names and tell the time.
//
// Three commands. The tar is `-xpf`, keeping permissions and flags: a userland
// extracted without them has a world-writable /etc and no schg on the files
// that want it. The two copies are what every FreeBSD jail runbook does at
// this point -- without resolv.conf a jail cloned from this template cannot
// reach a package mirror, and without localtime it logs in UTC while the host
// does not.
func (a *ReleaseAdapter) Extract(ctx context.Context, release, root string) (templates.Result, error) {
	archive := path.Join(a.media, release, "base.txz")
	result := templates.Result{Root: root, Command: "tar -xpf " + archive + " -C " + root}

	if _, err := a.runHost(ctx, system.Command{
		Name:    "tar",
		Args:    []string{"-xpf", archive, "-C", root},
		Timeout: extractTimeout,
	}); err != nil {
		return result, err
	}

	for _, file := range []string{"/etc/resolv.conf", "/etc/localtime"} {
		if _, err := a.runHost(ctx, system.Command{
			Name:    "cp",
			Args:    []string{file, path.Join(root, file)},
			Timeout: time.Minute,
		}); err != nil {
			// A host with no /etc/resolv.conf of its own is a host that
			// resolves names some other way, not a failure to extract. The
			// archive is unpacked either way, and that is what the state says.
			common.Logger(ctx, "templates").Warn("could not copy a host file into the template",
				"file", file, "root", root, "err", err)
		}
	}
	return result, nil
}

// manifestSHA256 reads the checksum for one file out of a release MANIFEST.
//
// The format is tab-separated, filename first and SHA256 second:
//
//	base.txz\t3768988b…\t30469\tbase\t"Base system (MANDATORY)"\ton
//
// Only the first two fields are read. The rest — file count, component name,
// description, default-on flag — belong to bsdinstall's installer menu.
func manifestSHA256(stdout, file string) (string, error) {
	for _, line := range nonEmptyLines(stdout) {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[0] != file {
			continue
		}
		if sum := strings.TrimSpace(fields[1]); sum != "" {
			return sum, nil
		}
	}
	return "", common.NewAppError(http.StatusBadGateway,
		"the release MANIFEST names no checksum for "+file)
}

var _ templates.ReleaseSource = (*ReleaseAdapter)(nil)
