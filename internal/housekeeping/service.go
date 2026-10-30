package housekeeping

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
)

// DefaultSnapshotAge is how old a snapshot has to be, in days, before it is
// listed, unless the request asks for another age.
const DefaultSnapshotAge = 30

// otherTools are the snapshot name prefixes of the tools that take and prune
// snapshots on their own schedule. Their snapshots are theirs: removing one
// breaks an incremental send (zrepl) or a retention count (sanoid,
// zfs-auto-snapshot). Measured on isengard: most snapshots under
// <pool>/jails are zrepl's.
var otherTools = []string{"zrepl_", "autosnap_", "zfs-auto-snap"}

type Service struct {
	templates TemplateSource
	images    ImageSource
	retained  RetainedSource
	snapshots SnapshotSource
	archives  ArchiveSource
	now       func() time.Time
}

// Sources are what the service reads. Any can be nil, and is then left out,
// as on a host without vm-bhyve there are no images.
type Sources struct {
	Templates TemplateSource
	Images    ImageSource
	Retained  RetainedSource
	Snapshots SnapshotSource
	Archives  ArchiveSource
}

func NewService(sources Sources) *Service {
	return &Service{templates: sources.Templates, images: sources.Images, retained: sources.Retained,
		snapshots: sources.Snapshots, archives: sources.Archives, now: time.Now}
}

// Reclaimable reads every source and lists what nothing uses.
func (s *Service) Reclaimable(ctx context.Context, snapshotAge int) (Report, error) {
	if snapshotAge < 1 || snapshotAge > 3650 {
		return Report{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"older_than_days is %d; it has to be from 1 to 3650", snapshotAge))
	}
	report := Report{Items: []Item{}, Kinds: map[string]int64{}, SnapshotAge: snapshotAge,
		SkippedSnapshots: map[string]int{}, Problems: []string{}}
	problem := func(what string, err error) {
		report.Problems = append(report.Problems, what+": "+err.Error())
	}

	if s.templates != nil {
		templates, err := s.templates.Templates(ctx)
		if err != nil {
			problem("templates", err)
		}
		for _, t := range templates {
			if t.Busy || t.Dependents > 0 {
				continue
			}
			why := "no jail or template is cloned from it"
			if !t.Ready {
				why = "not ready, and nothing is cloned from it: a preparation that stopped part way"
			}
			report.add(Item{Kind: KindTemplate, Name: t.Name, Size: t.Used, Why: why,
				Remove: remove("/api/templates/", t.Name, t.Name)})
		}
	}

	if s.images != nil {
		images, err := s.images.Images(ctx)
		if err != nil {
			problem("VM images", err)
		}
		for _, i := range images {
			if i.Busy || i.Guests > 0 {
				continue
			}
			why := "no guest's disk is cloned from it"
			if !i.Ready {
				why = "not ready, and no guest uses it: a write that stopped part way"
			}
			report.add(Item{Kind: KindImage, Name: i.Name, Size: i.Used, Why: why,
				Remove: remove("/api/vm-images/", i.Name, i.Name)})
		}
	}

	if s.retained != nil {
		retained, err := s.retained.Retained(ctx)
		if err != nil {
			problem("retained datasets", err)
		}
		for _, r := range retained {
			report.add(Item{Kind: KindRetained, Name: r.Name, Size: r.Used,
				Why:    "kept past its jail (" + r.Dataset + "); a restore would bring it back",
				Remove: remove("/api/retained/", r.Name, r.Name)})
		}
	}

	if s.snapshots != nil {
		snapshots, errs := s.snapshots.JailSnapshots(ctx)
		for _, err := range errs {
			problem("snapshots", err)
		}
		cutoff := s.now().AddDate(0, 0, -snapshotAge)
		for _, snap := range snapshots {
			if !snap.Created.Before(cutoff) {
				continue
			}
			if tool := otherTool(snap.Short); tool != "" {
				report.SkippedSnapshots[tool]++
				continue
			}
			created := snap.Created
			days := int(s.now().Sub(created).Hours() / 24)
			report.add(Item{Kind: KindSnapshot, Name: snap.Name, Jail: snap.Jail, Size: snap.Used,
				Created: &created, Why: fmt.Sprintf("taken %d days ago", days),
				Remove: remove("/api/jails/"+url.PathEscape(snap.Jail)+"/snapshots/", snap.Short, snap.Name)})
		}
	}

	if s.archives != nil {
		archives, err := s.archives.Archives(ctx)
		if err != nil {
			problem("downloaded archives", err)
		}
		for _, a := range archives {
			modified := a.Modified
			report.add(Item{Kind: KindArchive, Name: a.Path, Size: a.Size, Created: &modified,
				Why: "downloaded for " + a.For + "; preparing it again downloads it again"})
		}
	}

	sort.SliceStable(report.Items, func(i, j int) bool {
		if report.Items[i].Kind != report.Items[j].Kind {
			return kindOrder(report.Items[i].Kind) < kindOrder(report.Items[j].Kind)
		}
		return report.Items[i].Size > report.Items[j].Size
	})
	return report, nil
}

func (r *Report) add(item Item) {
	r.Items = append(r.Items, item)
	r.Total += item.Size
	r.Kinds[item.Kind] += item.Size
}

func remove(prefix, name, confirm string) *Request {
	return &Request{Method: http.MethodDelete,
		Path: prefix + url.PathEscape(name) + "?confirm=" + url.QueryEscape(confirm)}
}

func otherTool(short string) string {
	for _, prefix := range otherTools {
		if strings.HasPrefix(short, prefix) {
			return prefix
		}
	}
	return ""
}

func kindOrder(kind string) int {
	for i, k := range []string{KindTemplate, KindImage, KindRetained, KindSnapshot, KindArchive} {
		if k == kind {
			return i
		}
	}
	return 99
}
