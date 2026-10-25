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
	StateFetched    = "fetched"
	StateExtracting = "extracting"
	// StateExtracted is a dataset with a userland and no @base: a template
	// that exists and is not usable. A failed derive leaves this too, and so
	// does an extract that stopped half way -- the filesystem cannot tell a
	// half-unpacked userland from a whole one, which is why neither is built
	// on top of.
	StateExtracted = "extracted"
	StateUpdating  = "updating"
	StateReady     = "ready"
)

const (
	OperationFetchBase = "fetch-base"
	OperationExtract   = "extract-base"
	OperationUpdate    = "update-template"
	// OperationSeal is only logged when sealing fails: the snapshot itself is
	// already audited by the storage domain as create-snapshot, and logging it
	// twice would put one act in the operations list under two names.
	OperationSeal = "seal-template"
)

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
	// Extract unpacks a fetched release into root, and leaves the userland
	// able to resolve names and tell the time -- the host's resolv.conf and
	// localtime, which every runbook copies in at this point and without which
	// a jail cloned from the template cannot reach a package mirror.
	Extract(ctx context.Context, release, root string) (Result, error)
	// Update brings an extracted userland to the release's current patch
	// level with freebsd-update, against the directory rather than a running
	// system. Reports what it ran; the patch level it reached is in the
	// command's own output and in the audit log, not recorded here.
	Update(ctx context.Context, release, root string) (Result, error)
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
	if current, ok := byName(existing, name); ok {
		if current.State == StateReady {
			// Already there and usable. The answer to a client that timed out
			// and repeated the call (JD-016), not a conflict.
			return current, nil
		}
		if current.Dataset != "" && current.Running == nil {
			// A dataset with no @base is a stage that stopped: a derive that
			// failed, or an extract that did. Unpacking a second userland over
			// the first would make a mess nothing can describe afterwards, and
			// the filesystem cannot tell a half-unpacked tree from a whole one.
			return Template{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s already has a dataset and no @%s snapshot, which is what a stage that "+
					"stopped leaves behind; destroy it and prepare it again rather than "+
					"unpacking on top of it — the release archive is kept, so only the "+
					"unpacking is repeated",
				name, ReadySnapshot))
		}
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

	if !s.fetch(ctx, name, release, arch) {
		// Left where it stopped. The next GET reads the filesystem and reports
		// the stage that did complete; the audit log says why this one did not.
		return
	}
	root, ok := s.extract(ctx, name, release)
	if !ok {
		return
	}
	if !s.update(ctx, name, release, root) {
		return
	}
	s.seal(ctx, name)
}

// fetch is the first stage, skipped when the archive is already there: that is
// what makes repeating a timed-out call resume rather than start again.
func (s *Service) fetch(ctx context.Context, name, release, arch string) bool {
	if fetched, err := s.releases.Fetched(ctx); err == nil && containsString(fetched, release) {
		return true
	}
	s.running.step(name, StateFetching, "fetching "+release+" base.txz")
	result, err := s.releases.FetchBase(ctx, release, arch)
	s.logOperation(ctx, OperationFetchBase, name, result.Command, err)
	return err == nil
}

// extract is the second: an empty dataset, then the archive unpacked into it.
//
// A failed extract leaves a dataset holding half a userland, which looks
// exactly like a complete one -- the filesystem cannot tell them apart, and
// JD-008's state machine therefore cannot either. So this never extracts into
// a dataset that already exists: that is the same refusal Derive makes, for
// the same reason, and the operator destroys it rather than having a second
// userland written over the first.
func (s *Service) extract(ctx context.Context, name, release string) (string, bool) {
	s.running.step(name, StateExtracting, "unpacking "+release+" into the template")

	root, err := s.storage.Create(ctx, name)
	if err != nil {
		s.logOperation(ctx, OperationExtract, name, "", err)
		return "", false
	}
	result, err := s.releases.Extract(ctx, release, root)
	s.logOperation(ctx, OperationExtract, name, result.Command, err)
	return root, err == nil
}

// update is the third stage: freebsd-update against the directory, bringing a
// freshly unpacked release to its current patch level. base.txz is the release
// as published, so a template built from it is behind from the moment it is
// extracted -- every security advisory since.
//
// Nothing records which patch level it reached. freebsd-update says so in its
// own output, which the audit log keeps, and `freebsd-version -u` inside a jail
// cloned from the template is the live answer. A number stored here could
// disagree with the userland it describes (JD-011).
func (s *Service) update(ctx context.Context, name, release, root string) bool {
	s.running.step(name, StateUpdating, "bringing "+release+" to its current patch level")

	result, err := s.releases.Update(ctx, release, root)
	s.logOperation(ctx, OperationUpdate, name, result.Command, err)
	return err == nil
}

// seal takes the @base snapshot, which is the whole of what makes a template
// usable (JD-007). It is last for that reason: until it runs, every stage
// before it has left something that is honestly not ready.
func (s *Service) seal(ctx context.Context, name string) bool {
	s.running.step(name, StateUpdating, "snapshotting the finished template")

	err := s.storage.Seal(ctx, name)
	if err != nil {
		s.logOperation(ctx, OperationSeal, name, "", err)
	}
	return err == nil
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
