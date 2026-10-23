package storage

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// createProperties is the allowlist for zfs create. Five of the sixty-odd
// properties this ZFS reports as editable, because an allowlist exists so that
// widening it is a deliberate, reviewed act rather than whatever ZFS happens to
// permit -- and what it happens to permit includes sharenfs, which would export
// a jail's data over the network, and canmount, which would stop it mounting.
//
// Sorted, because the whole list is printed in the error a rejected property
// gets and an operator reads that list looking for a name.
var createProperties = []string{
	"atime",
	"compression",
	"mountpoint",
	"recordsize",
	"refquota",
}

// updateProperties is the same list without mountpoint. Growing a quota is an
// ordinary operational need with no other path through this API; moving a
// dataset's mountpoint while a jail is running against it leaves the jail
// looking at a directory that is no longer backed by the dataset it was. So
// mountpoint is settable exactly once, when the dataset does not yet exist.
var updateProperties = []string{
	"atime",
	"compression",
	"recordsize",
	"refquota",
}

// checkNativeProperties rejects anything outside allowed. Values are not
// checked at all: zfs knows what a valid recordsize is, says so precisely when
// it is wrong, and that message reaches the client (JD-012). Re-implementing
// "512 to 16M, power of 2" here would only add a second opinion that can be
// wrong on its own.
func checkNativeProperties(properties map[string]string, allowed []string, what string) error {
	for name := range properties {
		if !slices.Contains(allowed, name) {
			return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
				"%q cannot be set %s; allowed: %s", name, what, strings.Join(allowed, ", ")))
		}
	}
	return nil
}

// checkUserProperty rejects anything that is not a ZFS user property.
//
// The colon is ZFS's own rule for telling a user property from a native one, so
// a name without one would either be refused by zfs or, worse, accepted as a
// native property that bypassed the allowlist above. The equals sign is this
// code's rule: zfs set takes property=value as a single argument, so a name
// containing one would be split in a place nobody intended.
//
// Nothing else is checked, and the value is not checked at all. Jail Deck
// passes user properties through and never interprets them (JD-011): what a
// client puts in otter:instance is the client's business.
func checkUserProperty(name string) error {
	switch {
	case name == "":
		return common.NewAppError(http.StatusBadRequest, "a property name cannot be empty")
	case !strings.Contains(name, ":"):
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%q is not a user property: a user property's name contains a colon, as in otter:instance", name))
	case strings.ContainsAny(name, "= \t\n\r"):
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%q cannot be used as a property name: it contains an equals sign or whitespace", name))
	}
	return nil
}

// checkChildName validates one dataset name component.
//
// ZFS allows alphanumerics and the four punctuation characters below, and
// requires the name to begin with an alphanumeric. A slash is refused rather
// than passed through: it would make a nested dataset, which zfs create only
// makes with -p, and the route below takes one path segment anyway. Nesting is
// two calls, or a later decision.
func checkChildName(child string) error {
	if child == "" {
		return common.NewAppError(http.StatusBadRequest, "a dataset name cannot be empty")
	}
	if child == "." || child == ".." {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf("%q is not a dataset name", child))
	}
	if !isAlphanumeric(child[0]) {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"dataset name %q must begin with a letter or a digit", child))
	}
	for i := 0; i < len(child); i++ {
		c := child[i]
		if isAlphanumeric(c) || c == '_' || c == '-' || c == ':' || c == '.' {
			continue
		}
		if c == '/' {
			return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
				"dataset name %q may not contain a slash: create one level at a time", child))
		}
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"dataset name %q contains a character ZFS does not allow at offset %d", child, i))
	}
	return nil
}

func isAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
