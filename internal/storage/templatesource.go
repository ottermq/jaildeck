package storage

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/templates"
)

// TemplateChild is where templates live under <pool>/jails, beside containers,
// media and retained (JD-006).
const TemplateChild = "templates"

// TemplateSource implements templates.TemplateStorage. It lives here for the
// same reason JailSource does: the side that implements a port depends on the
// side that defines it (JD-010), so internal/templates imports nothing from
// here.
type TemplateSource struct {
	service *Service
}

func NewTemplateSource(service *Service) *TemplateSource {
	return &TemplateSource{service: service}
}

// Templates reads everything a template is from ZFS: the dataset, whether its
// @base snapshot exists, and what it was cloned from. Nothing is stored
// (JD-011), so nothing can disagree with the host.
func (s *TemplateSource) Templates(ctx context.Context) ([]templates.Template, error) {
	roots, err := s.service.roots(ctx)
	if err != nil {
		return nil, err
	}

	found := make([]templates.Template, 0)
	for _, root := range roots {
		parent := templateParent(root)
		datasets, err := s.service.system.DatasetsUnder(ctx, []string{parent})
		if err != nil {
			// A pool with no templates dataset: zfs list exits non-zero, which
			// is an answer rather than a failure.
			continue
		}
		snapshots, err := s.service.system.Snapshots(ctx, []string{parent})
		if err != nil {
			snapshots = nil
		}

		for _, dataset := range datasets {
			name, ok := directChild(dataset.Name, parent)
			if !ok {
				continue
			}
			template := templates.Template{
				Name: name, Dataset: dataset.Name, Mountpoint: dataset.Mountpoint,
				Used: dataset.Used, Referenced: dataset.Referenced,
			}
			ready := dataset.Name + "@" + templates.ReadySnapshot
			for _, snapshot := range snapshots {
				if snapshot.Name == ready {
					template.Ready, template.Snapshot = true, ready
				}
			}
			// The lineage, read rather than recorded. A derived template's
			// origin is its parent's snapshot; a release template has none.
			if origin, err := s.service.system.Origin(ctx, dataset.Name); err == nil && origin != "" {
				if from, _, cut := strings.Cut(origin, "@"); cut {
					if parentName, isTemplate := directChild(from, parent); isTemplate {
						template.Parent = parentName
					}
				}
			}
			found = append(found, template)
		}
	}
	return found, nil
}

// Create makes an empty template dataset and returns where it is mounted,
// which is where a release archive is extracted.
//
// Unlike Derive it clones nothing: a release template starts as an empty
// dataset and becomes a userland by having base.txz unpacked into it. The pool
// is the configured one, because there is no parent snapshot to take it from.
func (s *TemplateSource) Create(ctx context.Context, name string) (string, error) {
	roots, err := s.service.roots(ctx)
	if err != nil {
		return "", err
	}
	if len(roots) != 1 {
		return "", common.NewAppError(http.StatusConflict,
			"which pool a new template belongs on is not a guess Jail Deck makes; set JAILDECK_ZFS_POOL")
	}
	target := templateParent(roots[0]) + "/" + name

	if _, err := s.service.EnsureDataset(ctx, target, nil); err != nil {
		return "", err
	}
	dataset, err := s.service.datasetNamed(ctx, target)
	if err != nil || dataset == nil {
		return "", common.NewAppError(http.StatusInternalServerError,
			"created "+target+" but cannot find where it is mounted")
	}
	return dataset.Mountpoint, nil
}

func (s *TemplateSource) Derive(ctx context.Context, parentSnapshot, name string) (string, error) {
	pool, _, _ := strings.Cut(parentSnapshot, "/")
	target := templateParent(pool) + "/" + name

	result, err := s.service.system.CloneSnapshot(ctx, parentSnapshot, target)
	s.service.logWrite(ctx, OperationCloneDataset, result, err)
	if err != nil {
		return "", toWriteError("clone "+parentSnapshot+" to", target, err)
	}

	dataset, err := s.service.datasetNamed(ctx, target)
	if err != nil || dataset == nil {
		return "", common.NewAppError(http.StatusInternalServerError,
			"cloned "+target+" but cannot find where it is mounted")
	}
	return dataset.Mountpoint, nil
}

// Seal takes the @base snapshot that marks a template usable. Not recursive: a
// template has no children, and -r would be an opinion about a shape that does
// not exist.
func (s *TemplateSource) Seal(ctx context.Context, name string) error {
	dataset, err := s.datasetFor(ctx, name)
	if err != nil {
		return err
	}
	_, err = s.service.CreateSnapshot(ctx, dataset, templates.ReadySnapshot, false)
	return err
}

func templateParent(pool string) string {
	return pool + "/jails/" + TemplateChild
}

// directChild is the name of a dataset immediately under parent, and whether
// it is one at all. A template's own child datasets, if any ever existed,
// would not be templates.
func directChild(dataset, parent string) (string, bool) {
	rest, under := strings.CutPrefix(dataset, parent+"/")
	if !under || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

var _ templates.TemplateStorage = (*TemplateSource)(nil)

// FromJail turns a configured jail into a template every later jail can be
// cloned from -- Handbook 17.4.3, "Cloning a Jail as a Template" (JD-027).
//
// Four commands:
//
//	zfs snapshot <jail>@base
//	zfs clone    <jail>@base  templates/<name>
//	zfs promote  templates/<name>
//
// The promote is the one that is easy to leave out and expensive to leave out.
// Without it the template is a clone OF the jail, so the jail can never be
// destroyed -- zfs answers "filesystem has dependent clones" and names the
// template. After it, the jail is a clone of the template instead, which is
// also the right way round: a template should outlive the jail it came from,
// and the lineage reads release template -> this one -> the jail, which is
// exactly JD-007's model.
//
// The jail must already be stopped. "Stop the jail before snapshotting so the
// image is consistent" is the Handbook's own instruction, and whether a jail is
// running is the jails domain's to know.
func (s *TemplateSource) FromJail(ctx context.Context, jailDataset, name string) (string, error) {
	pool, _, _ := strings.Cut(jailDataset, "/")
	target := templateParent(pool) + "/" + name

	if existing, err := s.service.datasetNamed(ctx, target); err == nil && existing != nil {
		return "", common.NewAppError(http.StatusConflict, target+" already exists")
	}

	// Named base from the start, so that after the promote the template owns a
	// snapshot already called @base and no second one is needed.
	snapshot := jailDataset + "@" + templates.ReadySnapshot

	// Refused rather than reused if the jail already carries one. CreateSnapshot
	// is idempotent by design (JD-024), so without this check an existing @base
	// would be silently accepted -- and the template would then capture whatever
	// state that older snapshot holds rather than the jail as it is now. The
	// name is a template's convention; a jail wearing it is worth stopping for.
	existingSnapshots, err := s.service.SnapshotsOf(ctx, jailDataset)
	if err != nil {
		return "", err
	}
	for _, found := range existingSnapshots {
		if found.Name == snapshot {
			return "", common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s already exists, and reusing it would make a template of whenever it was "+
					"taken rather than of the jail as it is now; destroy it first", snapshot))
		}
	}

	if _, err := s.service.CreateSnapshot(ctx, jailDataset, templates.ReadySnapshot, false); err != nil {
		return "", err
	}

	clone, cloneErr := s.service.system.CloneSnapshot(ctx, snapshot, target)
	s.service.logWrite(ctx, OperationCloneDataset, clone, cloneErr)
	if cloneErr != nil {
		return "", toWriteError("clone "+snapshot+" to", target, cloneErr)
	}

	promote, promoteErr := s.service.system.PromoteDataset(ctx, target)
	s.service.logWrite(ctx, OperationPromoteDataset, promote, promoteErr)
	if promoteErr != nil {
		// The template exists and the jail is pinned by it. Reported rather
		// than unwound: zfs promote failing is not a state to guess at.
		return "", toWriteError("promote", target, promoteErr)
	}
	return target, nil
}

// Dependents asks ZFS what is holding the template in place, across every one
// of its snapshots rather than just @base: a template derived from this one
// hangs off whichever snapshot it was cloned from.
func (s *TemplateSource) Dependents(ctx context.Context, name string) (templates.Dependents, error) {
	dataset, err := s.datasetFor(ctx, name)
	if err != nil {
		return templates.Dependents{}, err
	}
	snapshots, err := s.service.SnapshotsOf(ctx, dataset)
	if err != nil {
		return templates.Dependents{}, err
	}

	found := templates.Dependents{Clones: make([]string, 0), Holds: make([]string, 0)}
	for _, snapshot := range snapshots {
		if snapshot.Dataset != dataset {
			continue
		}
		clones, err := s.service.system.Clones(ctx, snapshot.Name)
		if err != nil {
			return templates.Dependents{}, toWriteError("read the clones of", snapshot.Name, err)
		}
		found.Clones = append(found.Clones, clones...)

		holds, err := s.service.system.Holds(ctx, snapshot.Name)
		if err != nil {
			return templates.Dependents{}, toWriteError("read the holds on", snapshot.Name, err)
		}
		for _, hold := range holds {
			found.Holds = append(found.Holds, hold.Snapshot+" ("+hold.Tag+")")
		}
	}
	return found, nil
}

// Destroy removes the template and its snapshots.
//
// -r because a template always has at least @base and zfs refuses a bare
// destroy then. Never -R, which would take the dependent clones this refuses
// to touch -- the service has already established there are none, and passing
// -R would make that check decorative.
func (s *TemplateSource) Destroy(ctx context.Context, name string) error {
	dataset, err := s.datasetFor(ctx, name)
	if err != nil {
		return err
	}
	result, destroyErr := s.service.system.DestroyDataset(ctx, dataset, true)
	s.service.logWrite(ctx, OperationDestroyDataset, result, destroyErr)
	if destroyErr != nil {
		return toWriteError("destroy", dataset, destroyErr)
	}
	return nil
}

// datasetFor finds the template's dataset on whichever pool has it.
func (s *TemplateSource) datasetFor(ctx context.Context, name string) (string, error) {
	roots, err := s.service.roots(ctx)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		candidate := templateParent(root) + "/" + name
		if dataset, err := s.service.datasetNamed(ctx, candidate); err == nil && dataset != nil {
			return candidate, nil
		}
	}
	return "", common.NewAppError(http.StatusNotFound, "no template named "+name)
}
