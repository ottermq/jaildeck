package fake

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/templates"
)

// ReleaseSource is an in-memory FreeBSD mirror. It fetches nothing and
// remembers what it was asked for, so the detached prepare flow can be
// exercised without a network or a FreeBSD host.
//
// "15.0-RELEASE" always fails, because a template left half prepared by a
// failed fetch is a state worth being able to see.
type ReleaseSource struct {
	mu        sync.Mutex
	fetched   []string
	extracted []string
	updated   []string
	// delay holds FetchBase open, so a test can observe the in-flight state
	// the real thing spends minutes in.
	delay time.Duration
}

// fakeFetchSeconds makes a fake fetch take time, so running the application in
// fake mode shows the in-flight state a real one spends minutes in. Zero, the
// default, returns at once -- which is what the tests want, and why this is an
// environment variable rather than a constant.
const fakeFetchSeconds = "JAILDECK_FAKE_FETCH_SECONDS"

func NewReleaseSource() *ReleaseSource {
	source := &ReleaseSource{}
	if seconds, err := strconv.Atoi(os.Getenv(fakeFetchSeconds)); err == nil && seconds > 0 {
		source.delay = time.Duration(seconds) * time.Second
	}
	return source
}

// SetDelay makes a fetch take time. Zero returns immediately.
func (s *ReleaseSource) SetDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// Host is the release this fake's jails and templates were seeded against.
func (s *ReleaseSource) Host(context.Context) (templates.Host, error) {
	return templates.Host{Release: "15.1-RELEASE", Arch: "amd64"}, nil
}

func (s *ReleaseSource) Fetched(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.fetched...), nil
}

func (s *ReleaseSource) FetchBase(ctx context.Context, release, arch string) (templates.Result, error) {
	s.mu.Lock()
	delay := s.delay
	s.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return templates.Result{}, ctx.Err()
		}
	}

	archive := "/fake/jail/media/" + release + "/base.txz"
	result := templates.Result{
		Root: archive,
		Command: "fetch -a -o " + archive +
			" https://download.freebsd.org/releases/" + arch + "/" + release + "/base.txz",
	}
	if strings.HasPrefix(release, "15.0-") {
		// Shaped like the real failure: fetch exits non-zero and the adapter
		// wraps that in a system.CommandError, whose exit code is what decides
		// whether the client sees 400 or 500.
		const stderr = "fetch: https://download.freebsd.org/releases/amd64/15.0-RELEASE/base.txz: Not Found"
		return result, &system.CommandError{
			Command: "fetch",
			Args:    []string{"-a", "-o", archive},
			Result:  system.CommandResult{Stderr: stderr, ExitCode: 1},
			Err:     errors.New("exit status 1: " + stderr),
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, already := range s.fetched {
		if already == release {
			return result, nil
		}
	}
	s.fetched = append(s.fetched, release)
	return result, nil
}

// Extract unpacks nothing and records that it was asked. The delay applies
// here too, so fake mode shows the second stage in flight as well as the first.
func (s *ReleaseSource) Extract(ctx context.Context, release, root string) (templates.Result, error) {
	s.mu.Lock()
	delay := s.delay
	s.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return templates.Result{}, ctx.Err()
		}
	}

	archive := "/fake/jail/media/" + release + "/base.txz"
	result := templates.Result{Root: root, Command: "tar -xpf " + archive + " -C " + root}
	if strings.HasPrefix(release, "14.0-") {
		// A second failure mode, at the stage after the download: the archive
		// is there and unpacking it is what goes wrong.
		const stderr = "tar: Error opening archive: Unrecognized archive format"
		return result, &system.CommandError{
			Command: "tar",
			Args:    []string{"-xpf", archive, "-C", root},
			Result:  system.CommandResult{Stderr: stderr, ExitCode: 1},
			Err:     errors.New("exit status 1: " + stderr),
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.extracted = append(s.extracted, release)
	return result, nil
}

// Update patches nothing and records that it was asked.
//
// "14.1-" always fails, so the third stage has a failure of its own: a
// template that unpacked cleanly and could not be brought to a patch level is
// a different thing to look at from one whose archive would not open.
func (s *ReleaseSource) Update(ctx context.Context, release, root string) (templates.Result, error) {
	s.mu.Lock()
	delay := s.delay
	s.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return templates.Result{}, ctx.Err()
		}
	}

	result := templates.Result{
		Root: root,
		Command: "freebsd-update -b " + root + " -d /fake/jail/media/" + release +
			"/freebsd-update --currently-running " + release + " --not-running-from-cron fetch install",
	}
	if strings.HasPrefix(release, "14.1-") {
		const stderr = "freebsd-update: Cannot fetch metadata from update.FreeBSD.org"
		return result, &system.CommandError{
			Command: "freebsd-update",
			Args:    []string{"-b", root},
			Result:  system.CommandResult{Stderr: stderr, ExitCode: 1},
			Err:     errors.New("exit status 1: " + stderr),
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.updated = append(s.updated, release)
	return result, nil
}

var _ templates.ReleaseSource = (*ReleaseSource)(nil)
