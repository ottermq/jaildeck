package templates

import (
	"context"
	"fmt"
	"net/http"
	"regexp"

	"github.com/ottermq/jaildeck/internal/common"
)

// The states a template reports. Every one but the -ing forms is read off the
// filesystem (JD-008, JD-011): the archive is in media/ or it is not, the
// dataset exists or it does not, the @base snapshot exists or it does not.
const (
	// StateAbsent is a release nothing has been done for yet. It is a state
	// rather than a 404 because an operation can be running against it.
	StateAbsent = "absent"
	// StateFetching and the other -ing forms come from the in-memory record of
	// what this process is doing, not from the disk.
	StateFetching = "fetching"
	// StateFetched is base.txz in media/ and no dataset yet.
	StateFetched = "fetched"
	// StateExtracted is a dataset with a userland and no @base: a template
	// that exists and is not usable. A failed derive leaves this too.
	StateExtracted = "extracted"
	StateReady     = "ready"
)

const OperationFetchBase = "fetch-base"

// Host is what the host itself is, which decides both the default release to
// prepare and the ceiling: a jail's userland must be the same as or older than
// the kernel it runs under, so support goes downward only.
type Host struct {
	// Release is normalised to the release, never the patch level:
	// 15.1-RELEASE, not 15.1-RELEASE-p5. The mirror publishes no -pN
	// directory, so the patch level would be a 404.
	Release string
	Arch    string
}

// ReleaseSource is the mirror side of preparing a release template, defined
// here and implemented in internal/system/freebsd (JD-010). The URL layout,
// the MANIFEST format and the checksum are the adapter's business; this
// package decides only when to fetch and what state that leaves behind.
type ReleaseSource interface {
	Host(ctx context.Context) (Host, error)
	// Fetched lists the releases whose base.txz is already in media/. One of
	// the facts a template's state is read from.
	Fetched(ctx context.Context) ([]string, error)
	// FetchBase downloads base.txz into media/ and verifies it against the
	// release's own MANIFEST before reporting success. Minutes of work: the
	// caller has already returned 202 (JD-008).
	FetchBase(ctx context.Context, release, arch string) (Result, error)
}

// releaseName is the shape the mirror publishes: 15.1-RELEASE, 14.3-RELEASE.
// Deliberately narrow -- this string becomes a URL path segment and a dataset
// name, and neither should be built from whatever arrived.
var releaseName = regexp.MustCompile(`^[0-9]+\.[0-9]+-(RELEASE|STABLE|CURRENT)$`)

func checkRelease(release string) error {
	if !releaseName.MatchString(release) {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%q is not a release: the mirror publishes 15.1-RELEASE, and never a patch level like 15.1-RELEASE-p5", release))
	}
	return nil
}

// Prepare starts building a release template from the mirror and returns at
// once, with the state the template is in as the work begins (JD-008).
//
// Nothing is remembered about the operation beyond this process: the stages it
// completes are facts on disk, so a repeat resumes from wherever it got to and
// a restart costs only the in-flight marker, which was already untrue by then.
func (s *Service) Prepare(ctx context.Context, name, release string) (Template, error) {
	if err := checkTemplateName(name); err != nil {
		return Template{}, err
	}
	if release == "" {
		host, err := s.releases.Host(ctx)
		if err != nil {
			return Template{}, err
		}
		release = host.Release
	}
	if err := checkRelease(release); err != nil {
		return Template{}, err
	}

	host, err := s.releases.Host(ctx)
	if err != nil {
		return Template{}, err
	}

	existing, err := s.List(ctx)
	if err != nil {
		return Template{}, err
	}
	if current, ok := byName(existing, name); ok && current.State == StateReady {
		// Already there and usable. The answer to a client that timed out and
		// repeated the call (JD-016), not a conflict.
		return current, nil
	}

	// start is the claim, so the check and the claim cannot interleave: two
	// requests arriving together have one of them refused rather than both
	// writing the same dataset.
	if !s.running.start(name, Running{State: StateFetching, Step: "fetching " + release + " base.txz"}) {
		return Template{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is already being prepared by this process; watch it rather than starting it again", name))
	}

	// Detached from the request on purpose: the client is about to be told 202
	// and go away, and the work must not stop when it does (JD-014).
	work := context.WithoutCancel(ctx)
	go s.prepare(work, name, release, host.Arch)

	// Reported from the same listing every later poll reads, rather than
	// assembled here: the first answer and the tenth should be the same shape.
	after, err := s.List(work)
	if err != nil {
		return Template{}, err
	}
	template, _ := byName(after, name)
	return template, nil
}

// prepare runs the stages. Each one is audited as it goes, so what happened is
// answerable from the operations log rather than from a status field this
// would otherwise have to invent and store (JD-008).
func (s *Service) prepare(ctx context.Context, name, release, arch string) {
	defer s.running.stop(name)

	result, err := s.releases.FetchBase(ctx, release, arch)
	s.logOperation(ctx, OperationFetchBase, name, result.Command, err)
	if err != nil {
		// Left where it stopped. The next GET reads the filesystem and reports
		// the stage that did complete; the audit log says why this one did not.
		return
	}
}

// stateOf reads a template's state from what exists, with the in-flight record
// as the only thing it cannot see (JD-008).
func (s *Service) stateOf(template Template, fetched []string) string {
	if running, ok := s.running.of(template.Name); ok {
		return running.State
	}
	switch {
	case template.Ready:
		return StateReady
	case template.Dataset != "":
		return StateExtracted
	case containsString(fetched, template.Name):
		return StateFetched
	default:
		return StateAbsent
	}
}

func containsString(all []string, want string) bool {
	for _, item := range all {
		if item == want {
			return true
		}
	}
	return false
}
