package storage

import (
	"context"
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
	roots, err := s.service.roots(ctx)
	if err != nil {
		return err
	}
	for _, root := range roots {
		target := templateParent(root) + "/" + name
		if dataset, err := s.service.datasetNamed(ctx, target); err == nil && dataset != nil {
			_, err := s.CreateSeal(ctx, target)
			return err
		}
	}
	return common.NewAppError(http.StatusNotFound, "no template named "+name)
}

func (s *TemplateSource) CreateSeal(ctx context.Context, dataset string) (SnapshotResult, error) {
	return s.service.CreateSnapshot(ctx, dataset, templates.ReadySnapshot, false)
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
