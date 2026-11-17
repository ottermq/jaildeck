// Package osversion holds the rules for FreeBSD version strings: what one
// looks like, and whether a jail's or a template's userland is behind the
// host's (#229). Nothing here touches the host; the FreeBSD adapter reads the
// strings and this package judges them.
package osversion

import (
	"context"
	"regexp"
	"strconv"
)

// pattern is a FreeBSD version as freebsd-version prints it: a release and a
// branch, and a patch level when there is one. 15.1-RELEASE-p6, 14.3-RELEASE,
// 16.0-CURRENT, 15.0-STABLE. Anything else is refused rather than guessed at,
// because a jail's copy of freebsd-version is written by whoever is root in
// that jail.
var pattern = regexp.MustCompile(`^([0-9]+\.[0-9]+-[A-Z]+)(?:-p([0-9]+))?$`)

// Version is a parsed version string.
type Version struct {
	// Release is the version without its patch level, "15.1-RELEASE". Two
	// versions are comparable only when their releases are equal.
	Release string
	// Patch is the patch level, 0 for a release with none.
	Patch int
}

// Parse reads a version string. ok is false for anything not shaped like one.
func Parse(s string) (Version, bool) {
	m := pattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	patch := 0
	if m[2] != "" {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			// Digits too many for an int: not a real patch level.
			return Version{}, false
		}
		patch = n
	}
	return Version{Release: m[1], Patch: patch}, true
}

// Status is how a userland compares with the host's.
type Status string

const (
	// Current is the same release and patch level as the host's userland.
	Current Status = "current"
	// Behind is the same release at a lower patch level: updating the host
	// did not update it, because a jail's userland is its own copy.
	Behind Status = "behind"
	// Ahead is the same release at a higher patch level, a template updated
	// after the host last was.
	Ahead Status = "ahead"
	// OtherRelease is a different release, a 14.3 jail on a 15.1 host. Patch
	// levels of different releases say nothing about each other.
	OtherRelease Status = "other-release"
	// Unknown is a version that could not be read or did not parse, on
	// either side of the comparison.
	Unknown Status = "unknown"
)

// Standing is a jail's or a template's userland, judged against the host's.
type Standing struct {
	// Version is the userland as read, "" when it could not be read or was
	// not shaped like a version.
	Version string `json:"version"`
	Status  Status `json:"status"`
	// Levels is how many patch levels behind or ahead, for those two
	// statuses only.
	Levels int `json:"levels,omitempty"`
	// Host is the host userland it was compared with, "" when that was not
	// known.
	Host string `json:"host,omitempty"`
}

// Compare judges a userland version against the host's. Either string may be
// empty or malformed; the answer is then Unknown, never a guess.
func Compare(host, userland string) Standing {
	standing := Standing{Status: Unknown}
	v, ok := Parse(userland)
	if ok {
		standing.Version = userland
	}
	h, hostOK := Parse(host)
	if hostOK {
		standing.Host = host
	}
	if !ok || !hostOK {
		return standing
	}
	switch {
	case v.Release != h.Release:
		standing.Status = OtherRelease
	case v.Patch < h.Patch:
		standing.Status, standing.Levels = Behind, h.Patch-v.Patch
	case v.Patch > h.Patch:
		standing.Status, standing.Levels = Ahead, v.Patch-h.Patch
	default:
		standing.Status = Current
	}
	return standing
}

// Host is the host's own versions.
type Host struct {
	// KernelRunning is the kernel booted, kern.osrelease.
	KernelRunning string `json:"kernel_running"`
	// KernelInstalled is the kernel on disk, the one the next boot loads.
	KernelInstalled string `json:"kernel_installed"`
	// Userland is the host's base system.
	Userland string `json:"userland"`
	// RebootPending is true when the installed kernel is not the running
	// one: an update was installed and the host has not been restarted, so
	// the fixes in it are not in effect yet.
	RebootPending bool `json:"reboot_pending"`
}

// NewHost builds the host's versions. A value that is not shaped like a
// version is dropped rather than passed on, and a reboot is pending only when
// both kernels were read and they differ.
func NewHost(running, installed, userland string) Host {
	clean := func(s string) string {
		if _, ok := Parse(s); ok {
			return s
		}
		return ""
	}
	h := Host{KernelRunning: clean(running), KernelInstalled: clean(installed), Userland: clean(userland)}
	h.RebootPending = h.KernelRunning != "" && h.KernelInstalled != "" && h.KernelRunning != h.KernelInstalled
	return h
}

// UserlandLine finds the version in the text of a freebsd-version script, the
// value of its USERLAND_VERSION="..." line. ok is false when there is no such
// line or its value is not shaped like a version.
func UserlandLine(script []byte) (string, bool) {
	m := userlandLine.FindSubmatch(script)
	if m == nil {
		return "", false
	}
	value := string(m[1])
	if _, ok := Parse(value); !ok {
		return "", false
	}
	return value, true
}

var userlandLine = regexp.MustCompile(`(?m)^USERLAND_VERSION="([^"\n]*)"\s*$`)

// Source reads versions off a host. The FreeBSD adapter implements it, and so
// does the fake one.
type Source interface {
	// HostVersions reads the host's own kernels and userland.
	HostVersions(ctx context.Context) (Host, error)
	// Userland reads the userland version of the FreeBSD tree at root,
	// without running anything in it (JD-060).
	Userland(ctx context.Context, root string) (string, error)
}

// Judge reads the host's versions once and each root's userland, and judges
// every root against the host. A root that cannot be read is Unknown, never
// an error: one jail with a broken /bin must not take the listing with it.
// The host's versions come back too, empty when they could not be read.
func Judge(ctx context.Context, source Source, roots []string) (Host, map[string]Standing) {
	host, _ := source.HostVersions(ctx)
	standings := make(map[string]Standing, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		if _, seen := standings[root]; seen {
			continue
		}
		userland, err := source.Userland(ctx, root)
		if err != nil {
			userland = ""
		}
		standings[root] = Compare(host.Userland, userland)
	}
	return host, standings
}
