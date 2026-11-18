package templates

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/osversion"
)

// OperationUpgradePackages is a refresh's pkg upgrade, audited like every
// other stage of a detached operation.
const OperationUpgradePackages = "upgrade-packages"

// Refresh builds a new template from another one's @base, brought to its
// release's current patch level with freebsd-update and with every package
// upgraded (#252). The parent is never changed, so the jails cloned from it
// are unaffected and it stays as the way back; moving a jail onto the new
// one is a separate reclone.
//
// Minutes of work, so it returns at once with the template's state, as
// Prepare does (JD-008). Refused for a template Jail Deck did not build: the
// refresh runs freebsd-update and pkg as root on the clone, and a template
// promoted from a jail holds whatever that jail's root left in it (JD-061).
func (s *Service) Refresh(ctx context.Context, name, parent string) (Template, error) {
	if err := checkTemplateName(name); err != nil {
		return Template{}, err
	}
	existing, err := s.List(ctx)
	if err != nil {
		return Template{}, err
	}
	if found, ok := byName(existing, name); ok {
		if found.Ready {
			// The answer to a client that timed out and repeated the call
			// (JD-016), not a conflict.
			return found, nil
		}
		if found.Running != nil {
			return Template{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s is already being built by this process; watch it rather than starting it again", name))
		}
		return Template{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s exists but has no @%s snapshot, which is what a refresh that stopped leaves behind; "+
				"destroy it and refresh again rather than building on top of it", name, ReadySnapshot))
	}

	from, ok := byName(existing, parent)
	if !ok {
		return Template{}, common.NewAppError(http.StatusNotFound, "no template named "+parent)
	}
	if !from.Ready {
		return Template{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s has no @%s snapshot, so it is not usable as a parent (JD-007)", parent, ReadySnapshot))
	}
	if !from.Refreshable() {
		return Template{}, notRefreshable(from)
	}
	// The release freebsd-update is told the tree is running: read from the
	// parent's @base (#251), the plain release without its patch level.
	release := ""
	if from.Userland != nil {
		if v, ok := osversion.Parse(from.Userland.Version); ok {
			release = v.Release
		}
	}
	if checkRelease(release) != nil {
		return Template{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"cannot tell which FreeBSD release %s's userland is, so there is nothing to tell freebsd-update", parent))
	}

	if !s.running.start(name, Running{State: StateUpdating, Step: "cloning " + parent}) {
		return Template{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is already being built by this process; watch it rather than starting it again", name))
	}
	work := context.WithoutCancel(ctx)
	go s.refresh(work, name, from.Snapshot, release)

	after, err := s.List(work)
	if err != nil {
		return Template{}, err
	}
	template, _ := byName(after, name)
	return template, nil
}

// refresh runs the stages, each audited as it goes. A stage that fails leaves
// the clone without @base, shown as not ready, as a failed derive does.
func (s *Service) refresh(ctx context.Context, name, parentSnapshot, release string) {
	defer s.running.stop(name)

	// The clone is audited by storage, as clone-dataset, failed or not.
	root, err := s.storage.Derive(ctx, parentSnapshot, name)
	if err != nil {
		return
	}
	if !s.update(ctx, name, release, root) {
		return
	}

	s.running.step(name, StateUpdating, "upgrading its packages")
	result, err := s.packages.Upgrade(ctx, root)
	s.logOperation(ctx, OperationUpgradePackages, name, result.Command, err)
	if err != nil {
		return
	}
	s.seal(ctx, name, BuiltRefreshed)
}

func notRefreshable(t Template) error {
	if t.Built == BuiltJail {
		return common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s was made from a jail, so its files are whatever that jail's root left in them; "+
				"a refresh runs freebsd-update and pkg as root on them, and is refused (JD-061)", t.Name))
	}
	return common.NewAppError(http.StatusConflict, fmt.Sprintf(
		"%s does not say how it was made (no %s property), so Jail Deck cannot tell whether it built the files "+
			"a refresh would run as root on. If it was prepared from a release or derived, mark it once by hand: "+
			"zfs set %s=derived %s", t.Name, BuiltProperty, BuiltProperty, t.Dataset))
}
