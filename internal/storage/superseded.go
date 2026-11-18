package storage

import (
	"context"
	"path"

	"github.com/ottermq/jaildeck/internal/templates"
)

// refuseSuperseded refuses a template dataset that has been superseded
// (#259), as the source of a new jail or a reclone. Read at every request,
// so clearing the mark with zfs inherit lifts the refusal at once.
//
// A dataset whose properties cannot be read is not refused: the snapshot
// was just found, and the mark is a guard against habit, not a safety check
// a failed read should turn into an outage.
func (s *Service) refuseSuperseded(ctx context.Context, dataset string) error {
	by := s.supersededBy(ctx, dataset)
	if by == "" {
		return nil
	}
	parent := path.Dir(dataset)
	newest := by
	seen := map[string]bool{dataset: true, parent + "/" + by: true}
	for range templates.SupersededChainLimit {
		after := s.supersededBy(ctx, parent+"/"+newest)
		if after == "" || seen[parent+"/"+after] {
			break
		}
		seen[parent+"/"+after] = true
		newest = after
	}
	return templates.SupersededError(path.Base(dataset), newest, dataset,
		parent+"/"+newest+"@"+templates.ReadySnapshot)
}

func (s *Service) supersededBy(ctx context.Context, dataset string) string {
	properties, err := s.UserPropertiesOf(ctx, []string{dataset})
	if err != nil {
		return ""
	}
	return properties[dataset][templates.SupersededProperty]
}
