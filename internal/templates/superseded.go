package templates

import (
	"fmt"
	"net/http"

	"github.com/ottermq/jaildeck/internal/common"
)

// SupersededChainLimit bounds how far a refusal follows superseded-by marks
// to the newest replacement. A chain is one step per refresh; the bound is
// only there so a mark set by hand into a loop cannot spin.
const SupersededChainLimit = 16

// SupersededError is the refusal for a superseded template used as a source
// (#259): a conflict naming the newest replacement, and the way to lift it.
// Shared by this package, which refuses a derive or a refresh, and by storage,
// which refuses a new jail or a reclone.
func SupersededError(name, newest, dataset, newestSnapshot string) error {
	return common.NewAppError(http.StatusConflict, fmt.Sprintf(
		"%s has been superseded by %s; use %s instead. Jails already cloned from %s are not affected. "+
			"To use it anyway, clear the mark on the host: zfs inherit %s %s",
		name, newest, newestSnapshot, name, SupersededProperty, dataset))
}

// newestReplacement follows t's superseded-by marks through the listing to
// the newest template, which is the one a refusal should point at. A mark
// naming a template that is gone ends the chain there.
func newestReplacement(existing []Template, t Template) Template {
	current := t
	seen := map[string]bool{t.Name: true}
	for range SupersededChainLimit {
		next, ok := byName(existing, current.SupersededBy)
		if !ok || seen[next.Name] {
			break
		}
		seen[next.Name] = true
		current = next
	}
	return current
}

// refuseSuperseded refuses t as a source when it has been superseded by a
// template that exists. A mark naming one that is gone, or t itself, refuses
// nothing (#284): it points at nothing a client could use instead, and
// refusing on it would leave t refused for good. Such a mark is left by a
// removal that could not move it on, or set by hand.
func refuseSuperseded(existing []Template, t Template) error {
	if t.SupersededBy == "" || t.SupersededBy == t.Name {
		return nil
	}
	if _, ok := byName(existing, t.SupersededBy); !ok {
		return nil
	}
	newest := newestReplacement(existing, t)
	return SupersededError(t.Name, newest.Name, t.Dataset, newest.Name)
}
