package jails

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// ChildDataset is a dataset created beneath the jail's own, as part of the
// jail (JD-007).
//
// Two things make a jail want one. A quota belongs on a child: a clone's
// referenced space includes the template's blocks, so a refquota on the
// jail's root would count the userland against the jail, while one on a
// child counts only what the jail wrote. And state in a child makes the root
// replaceable -- a jail whose data lives in a child can have its root
// re-cloned from a newer template with the child moved aside and back
// (JD-026).
type ChildDataset struct {
	// Name is the dataset's own name, directly beneath the jail's: "data"
	// becomes <pool>/jails/containers/<jail>/data. One level at a time, as
	// the dataset endpoints take it.
	Name string `json:"name"`
	// Properties are native ZFS properties, from the allowlist in JD-023:
	// mountpoint, refquota, recordsize, compression, atime. The same map the
	// PUT /api/jails/{name}/datasets/{child} body carries, so a dataset is
	// described the same way wherever it is asked for.
	//
	// mountpoint is a path on the *host*, and defaults to the one the child
	// inherits -- <jail path>/<name>, which is /<name> seen from inside the
	// jail. A dataset meant at /var/db/postgres inside a jail at
	// /usr/local/jails/containers/db1 asks for
	// /usr/local/jails/containers/db1/var/db/postgres.
	Properties map[string]string `json:"properties,omitempty"`
}

// normalizeChildren refuses what this domain can judge about a spec's child
// datasets before anything is created: a name that is not one, and the same
// dataset asked for twice.
//
// What a dataset name may hold is the storage domain's rule and is left to it
// (JD-012). Only the slash is refused here, because it is the one that would
// change which dataset is meant rather than merely be rejected.
func normalizeChildren(children []ChildDataset) ([]ChildDataset, error) {
	seen := make(map[string]bool, len(children))
	for i, child := range children {
		name := strings.TrimSpace(child.Name)
		if name == "" {
			return nil, badSpec(fmt.Sprintf("datasets[%d] has no name", i))
		}
		if strings.Contains(name, "/") {
			return nil, badSpec(fmt.Sprintf(
				"dataset name %q may not contain a slash: a jail's child datasets are one level beneath it", name))
		}
		if seen[name] {
			return nil, badSpec(fmt.Sprintf("datasets names %q twice", name))
		}
		seen[name] = true
		children[i].Name = name
	}
	return children, nil
}

// checkChildMountpoint refuses a child dataset mounted anywhere but inside
// the jail.
//
// Measured on isengard, 15.1-RELEASE-p5: ZFS creates such a dataset without
// complaint, the jail cannot see it -- and `zfs destroy -r` on the jail
// still takes it, because it is the jail's child whatever it is mounted
// over. So destroying the jail would destroy a dataset living somewhere else
// on the host, and the operator would read its name in `taken` without any
// reason to look twice.
//
// A dataset that is meant to outlive the jail has its own door: `retain`
// moves it out of the jail's dataset tree, which is what makes the destroy
// leave it alone (JD-007).
func checkChildMountpoint(jailPath, child string, properties map[string]string) error {
	mountpoint, asked := properties["mountpoint"]
	if !asked {
		// Inherited, so it lands under the jail by construction.
		return nil
	}
	if jailPath == "" {
		return ErrJailNotOnZFS
	}
	if within(jailPath, mountpoint) {
		return nil
	}
	return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
		"mountpoint %q is not inside %s, so %q would be a child of this jail that the jail cannot "+
			"see and that destroying the jail would destroy anyway. Mount it under the jail, or "+
			"keep the data outside the jail's datasets with POST /api/jails/{name}/datasets/{child}/retain",
		mountpoint, jailPath, child))
}

// within reports whether mountpoint is the jail's own path or beneath it.
// "none" and "legacy" are not paths and so are not inside anything, which is
// the right answer for both: an unmounted child is one the jail cannot see.
func within(jailPath, mountpoint string) bool {
	if !strings.HasPrefix(mountpoint, "/") {
		return false
	}
	clean := path.Clean(mountpoint)
	jailPath = path.Clean(jailPath)
	return clean == jailPath || strings.HasPrefix(clean, jailPath+"/")
}
