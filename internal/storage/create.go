package storage

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/jails"
)

// ContainerChild is where a jail's own dataset lives under <pool>/jails,
// beside media, templates and retained (JD-006, JD-021).
const ContainerChild = "containers"

// CheckTemplate refuses a template that cannot be cloned, without creating
// anything.
//
// It is the same question CheckRecloneSource asks, minus the one refusal that
// only a re-clone needs — a jail cannot be re-cloned from itself, but a new
// jail has no root to compare against yet.
func (s *JailSource) CheckTemplate(ctx context.Context, templateSnapshot string) error {
	dataset, snapshot, ok := strings.Cut(templateSnapshot, "@")
	if !ok || dataset == "" || snapshot == "" {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%q is not a snapshot name; it has to be dataset@snapshot", templateSnapshot))
	}
	snapshots, err := s.service.system.Snapshots(ctx, []string{dataset})
	if err != nil {
		return common.NewAppError(http.StatusNotFound, "no dataset named "+dataset)
	}
	for _, found := range snapshots {
		if found.Name == templateSnapshot {
			return nil
		}
	}
	return common.NewAppError(http.StatusNotFound, "no snapshot named "+templateSnapshot)
}

// CloneRoot creates a jail's root by cloning a template's snapshot into
// <pool>/jails/containers/<name>.
//
// The pool is read off the template rather than taken from configuration: the
// clone and its origin have to share a pool — ZFS cannot clone across one —
// so the template's pool is the only pool this can possibly use, and reading
// it leaves nothing to disagree about.
//
// Nothing checks whether the root already exists. `zfs clone` refuses a
// dataset that is there, with its own message, and a check here would answer
// a question that could change before the clone ran.
func (s *JailSource) CloneRoot(ctx context.Context, templateSnapshot, name string) (jails.Root, error) {
	dataset, _, ok := strings.Cut(templateSnapshot, "@")
	if !ok {
		return jails.Root{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%q is not a snapshot name; it has to be dataset@snapshot", templateSnapshot))
	}
	pool := poolOf(dataset)
	if pool == "" || pool == dataset {
		return jails.Root{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%q is not a dataset in a pool", dataset))
	}
	if _, err := s.service.ensureContainers(ctx, pool); err != nil {
		return jails.Root{}, err
	}

	root := containersParent(pool) + "/" + name
	result, cloneErr := s.service.system.CloneSnapshot(ctx, templateSnapshot, root)
	s.service.logWrite(ctx, OperationCloneDataset, result, cloneErr)
	if cloneErr != nil {
		return jails.Root{}, toWriteError("clone "+templateSnapshot+" to", root, cloneErr)
	}

	// Read the mountpoint back rather than composing it. A clone inherits it
	// from its new parent, so it is <pool>/jails' mountpoint plus two names on
	// an ordinary host — but JD-021 only fixes where the layout is *mounted*,
	// not that every pool mounts it in the same place, and the jail's path has
	// to be the one ZFS actually gave it.
	created, err := s.service.datasetNamed(ctx, root)
	if err != nil {
		return jails.Root{}, err
	}
	if created == nil {
		// zfs clone exited 0 and the dataset is not there. Nothing sensible
		// explains that, and a jail configured with no path would be worse.
		return jails.Root{}, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"cloned %s to %s, and %s cannot be read back", templateSnapshot, root, root))
	}
	return jails.Root{Dataset: created.Name, Mountpoint: created.Mountpoint}, nil
}

// ensureContainers creates <pool>/jails/containers the first time a jail needs
// it, the same way ensureRetained creates retained/. A host that ran
// `jaildeck init` already has it (JD-021); one that did not should not be told
// to go and run it before a jail can be made.
func (s *Service) ensureContainers(ctx context.Context, pool string) (string, error) {
	parent := containersParent(pool)
	existing, err := s.datasetNamed(ctx, parent)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return parent, nil
	}
	result, createErr := s.system.CreateDataset(ctx, parent, nil)
	s.logWrite(ctx, OperationCreateDataset, result, createErr)
	if createErr != nil {
		return "", toWriteError("create", parent, createErr)
	}
	return parent, nil
}

func containersParent(pool string) string {
	return pool + "/jails/" + ContainerChild
}

// RootOrigin names a jail's own dataset and the snapshot it was cloned from.
//
// The template is the one part of a jail's spec that is neither in its
// configuration nor in jls, and the clone's origin is where the host keeps
// it (JD-016). Read rather than remembered, like everything else here: a
// jail whose root was re-cloned from a newer template (JD-026) reports the
// template it actually came from, not the one it was created with.
//
// A root that is not a clone reports an empty origin. That is a fact about
// the dataset -- a jail whose root was made by hand has no template -- and
// the caller reports it as a difference rather than as a failure.
func (s *JailSource) RootOrigin(ctx context.Context, jailPath string) (string, string, error) {
	root, err := s.datasetFor(ctx, jailPath, "")
	if err != nil {
		return "", "", err
	}
	origin, err := s.service.system.Origin(ctx, root)
	if err != nil {
		return "", "", toWriteError("read the origin of", root, err)
	}
	return root, origin, nil
}

var _ jails.CloneOrigin = (*JailSource)(nil)
