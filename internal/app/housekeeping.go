package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ottermq/jaildeck/internal/housekeeping"
	"github.com/ottermq/jaildeck/internal/images"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/storage"
	"github.com/ottermq/jaildeck/internal/templates"
)

// The housekeeping sources (#125): each domain's own answer, read through
// its service, so that what counts as "used" is what that domain's removal
// would refuse over.

type housekeepingTemplates struct{ templates *templates.Service }

func (h housekeepingTemplates) Templates(ctx context.Context) ([]housekeeping.Template, error) {
	list, err := h.templates.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]housekeeping.Template, 0, len(list))
	for _, t := range list {
		if t.Dataset == "" {
			// Fetched, not extracted: an archive, which is listed as one.
			continue
		}
		detail, err := h.templates.Get(ctx, t.Name)
		if err != nil {
			return out, fmt.Errorf("%s: %w", t.Name, err)
		}
		out = append(out, housekeeping.Template{Name: t.Name, Dataset: t.Dataset, Used: t.Used, Ready: t.Ready,
			Busy: t.Running != nil, Dependents: len(detail.Dependents.Clones) + len(detail.Dependents.Holds)})
	}
	return out, nil
}

type housekeepingImages struct{ images *images.Service }

func (h housekeepingImages) Images(ctx context.Context) ([]housekeeping.Image, error) {
	listing, err := h.images.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]housekeeping.Image, 0, len(listing.Images))
	for _, i := range listing.Images {
		if i.Dataset == "" {
			continue
		}
		out = append(out, housekeeping.Image{Name: i.Name, Used: i.Used, Ready: i.Ready,
			Busy: i.Running != nil, Guests: len(i.Guests)})
	}
	return out, nil
}

type housekeepingRetained struct{ storage *storage.Service }

func (h housekeepingRetained) Retained(ctx context.Context) ([]housekeeping.Retained, error) {
	datasets, err := h.storage.RetainedDatasets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]housekeeping.Retained, 0, len(datasets))
	for _, d := range datasets {
		out = append(out, housekeeping.Retained{Name: d.Name[strings.LastIndex(d.Name, "/")+1:], Dataset: d.Name, Used: d.Used})
	}
	return out, nil
}

type housekeepingSnapshots struct{ jails *jails.JailService }

// JailSnapshots lists each jail's own dataset's snapshots. A snapshot of the
// own dataset is destroyed with its children's of the same name (JD-024), so
// its size is theirs summed.
func (h housekeepingSnapshots) JailSnapshots(ctx context.Context) ([]housekeeping.Snapshot, []error) {
	list, err := h.jails.List(ctx)
	if err != nil {
		return nil, []error{err}
	}
	var out []housekeeping.Snapshot
	var errs []error
	for _, jail := range list {
		own := ""
		for _, d := range jail.Datasets {
			if d.Mountpoint == jail.Path {
				own = d.Name
			}
		}
		if own == "" {
			continue
		}
		name, err := jails.NewJailName(jail.Name)
		if err != nil {
			continue
		}
		snapshots, err := h.jails.Snapshots(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", jail.Name, err))
			continue
		}
		used := map[string]int64{}
		for _, s := range snapshots {
			used[s.Short] += s.Used
		}
		for _, s := range snapshots {
			if s.Dataset == own {
				out = append(out, housekeeping.Snapshot{Jail: jail.Name, Name: s.Name, Short: s.Short,
					Used: used[s.Short], Created: s.Created})
			}
		}
	}
	return out, errs
}
