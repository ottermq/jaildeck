package storage

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

const OperationCloneDataset = "clone-dataset"

// The steps of a re-clone, named so a failure can say which one it reached.
// They are in the order they run, and the order is the safety property: every
// child leaves the jail before anything touches the root.
const (
	StepReadState       = "read-state"
	StepRetainChildren  = "retain-children"
	StepParkRoot        = "park-root"
	StepClone           = "clone"
	StepRestoreChildren = "restore-children"
)

// Reclone is what a re-clone did, or got as far as doing. It is returned
// filled in on failure as well as on success, because where the datasets ended
// up is the thing an operator needs most when a step failed (#6).
type Reclone struct {
	Root string `json:"root"`
	// From is the snapshot the jail's root was cloned from, empty when its
	// root was not a clone at all.
	From string `json:"from"`
	To   string `json:"to"`
	// OldRoot is where the previous root is parked. Nothing is destroyed by a
	// re-clone (JD-026): if the new template turns out to be wrong, this is
	// the userland the jail was running a moment ago.
	OldRoot string `json:"old_root,omitempty"`
	// Children are the child datasets, back under the jail.
	Children []string `json:"children"`
	// FailedAt names the step that failed, and Parked says where every dataset
	// that is no longer under the jail currently sits.
	FailedAt string   `json:"failed_at,omitempty"`
	Parked   []Parked `json:"parked,omitempty"`
}

// Parked is one dataset that has been moved out and not yet moved back.
type Parked struct {
	Was string `json:"was"`
	Now string `json:"now"`
}

// RecloneRoot replaces a jail's root with a fresh clone of a template snapshot,
// keeping the jail's child datasets.
//
// The jail must already be stopped: this moves the filesystem its processes are
// running on. Stopping it is the caller's, because only the jails domain knows
// how (JD-005).
//
// Nothing is destroyed. The old root is renamed into retained/ beside the
// children, so a re-clone onto a bad template costs a second call to undo
// rather than a restore from backup.
func (s *Service) RecloneRoot(ctx context.Context, root, templateSnapshot string) (Reclone, error) {
	result := Reclone{Root: root, To: templateSnapshot, Children: make([]string, 0), Parked: make([]Parked, 0)}

	// Everything that can be checked before anything moves, is. A template
	// that does not exist should cost nothing but an error.
	if err := s.CheckRecloneSource(ctx, root, templateSnapshot); err != nil {
		result.FailedAt = StepReadState
		return result, err
	}
	origin, err := s.system.Origin(ctx, root)
	if err != nil {
		result.FailedAt = StepReadState
		return result, toWriteError("read the origin of", root, err)
	}
	result.From = origin

	children, err := s.childrenOf(ctx, root)
	if err != nil {
		result.FailedAt = StepReadState
		return result, err
	}

	// Children first, and all of them, before the root is touched at all.
	// zfs destroy -r on a jail takes its children with it, and even though
	// this destroys nothing, a child still under the root would be renamed
	// along with it and end up inside the parked copy.
	// outstanding is what has left the jail and not come back. It is the
	// answer to the only question that matters when a step fails, so it is
	// maintained as its own thing rather than inferred from how far the loops
	// below got.
	outstanding := map[string]string{}
	parked := func() []Parked {
		// In the order the children were moved, so the list reads as a
		// sequence rather than as whatever order a map produced.
		out := make([]Parked, 0, len(outstanding))
		for _, child := range children {
			if now, ok := outstanding[child]; ok {
				out = append(out, Parked{Was: child, Now: now})
			}
		}
		if now, ok := outstanding[root]; ok {
			out = append(out, Parked{Was: root, Now: now})
		}
		return out
	}

	// Children first, and all of them, before the root is touched at all.
	for _, child := range children {
		name := recloneRetainName(root, child)
		if _, err := s.RetainDataset(ctx, child, name); err != nil {
			result.FailedAt, result.Parked = StepRetainChildren, parked()
			return result, err
		}
		outstanding[child] = retainedParent(poolOf(root)) + "/" + name
	}

	oldRoot := recloneRootName(root, origin)
	if _, err := s.RetainDataset(ctx, root, oldRoot); err != nil {
		result.FailedAt, result.Parked = StepParkRoot, parked()
		return result, err
	}
	result.OldRoot = retainedParent(poolOf(root)) + "/" + oldRoot
	outstanding[root] = result.OldRoot

	clone, cloneErr := s.system.CloneSnapshot(ctx, templateSnapshot, root)
	s.logWrite(ctx, OperationCloneDataset, clone, cloneErr)
	if cloneErr != nil {
		result.FailedAt, result.Parked = StepClone, parked()
		return result, toWriteError("clone "+templateSnapshot+" to", root, cloneErr)
	}
	// The root is back, under a new origin, so it is no longer outstanding:
	// where the OLD root went is reported as OldRoot, which is where it is
	// meant to be rather than somewhere a failure left it.
	delete(outstanding, root)

	// And now the children have somewhere to go back to.
	for _, child := range children {
		name := strings.TrimPrefix(outstanding[child], retainedParent(poolOf(root))+"/")
		if _, err := s.RestoreDataset(ctx, poolOf(root), name, child); err != nil {
			result.FailedAt, result.Parked = StepRestoreChildren, parked()
			return result, err
		}
		delete(outstanding, child)
		result.Children = append(result.Children, child)
	}
	return result, nil
}

// CheckRecloneSource refuses a re-clone that cannot work, without touching
// anything. Exported so the jails domain can ask before it stops the jail: a
// mistyped template name should cost an error, not an outage.
func (s *Service) CheckRecloneSource(ctx context.Context, root, templateSnapshot string) error {
	dataset, snapshot, ok := strings.Cut(templateSnapshot, "@")
	if !ok || dataset == "" || snapshot == "" {
		return common.NewAppError(http.StatusBadRequest,
			fmt.Sprintf("%q is not a snapshot name; it has to be dataset@snapshot", templateSnapshot))
	}
	if dataset == root {
		return common.NewAppError(http.StatusBadRequest,
			"a jail cannot be re-cloned from a snapshot of itself")
	}
	snapshots, err := s.system.Snapshots(ctx, []string{dataset})
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

// childrenOf is the datasets directly beneath the jail's root. Only the direct
// children: a grandchild moves with its parent, as zfs rename carries a whole
// subtree.
func (s *Service) childrenOf(ctx context.Context, root string) ([]string, error) {
	datasets, err := s.system.DatasetsUnder(ctx, []string{root})
	if err != nil {
		return nil, toWriteError("list the datasets under", root, err)
	}
	children := make([]string, 0, len(datasets))
	for _, dataset := range datasets {
		rest, under := strings.CutPrefix(dataset.Name, root+"/")
		if under && !strings.Contains(rest, "/") {
			children = append(children, dataset.Name)
		}
	}
	return children, nil
}

// recloneRetainName is <jail>-<child>, which is what a client would have
// chosen by hand and is unique per jail.
func recloneRetainName(root, child string) string {
	return lastComponent(root) + "-" + lastComponent(child)
}

// recloneRootName is <jail>-root-<the snapshot it came from>, so two upgrades
// of the same jail park under different names and the parked copy says what it
// is. A root that was not a clone has no snapshot to name, and is parked as
// <jail>-root.
func recloneRootName(root, origin string) string {
	name := lastComponent(root) + "-root"
	if _, snapshot, ok := strings.Cut(origin, "@"); ok && snapshot != "" {
		name += "-" + snapshot
	}
	return name
}

func poolOf(dataset string) string {
	pool, _, _ := strings.Cut(dataset, "/")
	return pool
}
