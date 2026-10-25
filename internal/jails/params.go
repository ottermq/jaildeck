package jails

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The jail(8) parameter allowlist (JD-018), and the API's security boundary.
//
// A denylist fails open the day jail(8) gains a parameter, so the rule is the
// other way round: a parameter not named here is refused, and the refusal
// names it. The list is meant to grow on demand -- one table, in one file,
// where adding to it is a visible decision rather than an oversight.
//
// Every entry carries its own rule, so a value out of range is refused as
// precisely as an unknown name. The type is the entry's, not the JSON's: a
// parameter is an integer because jail(8) says so, and a spec that sends a
// string for one is told which it should have sent.

type paramKind int

const (
	paramInt paramKind = iota
	paramEnum
	paramBool
)

type paramRule struct {
	kind paramKind
	// min and max bound a paramInt, inclusive.
	min, max int
	// values are the accepted strings of a paramEnum.
	values []string
	// why is added to a refusal of a value this rule does not take, where
	// the range alone does not explain itself.
	why string
}

var allowedParams = map[string]paramRule{
	// The ruleset number the operator loaded. Checked against the kernel's
	// loaded sets before the jail is created, because a number it does not
	// have gives the jail the host's whole /dev (JD-018).
	"devfs_ruleset": {kind: paramInt, min: 0, max: 65535},
	// -1 is the default, 3 is the strictest kern.securelevel a jail takes.
	"securelevel": {kind: paramInt, min: -1, max: 3},
	// 0 shows every mount, 2 shows only the jail's own.
	"enforce_statfs": {kind: paramInt, min: 0, max: 2},
	"children.max": {kind: paramInt, min: 0, max: 0,
		why: "a jail that can create jails can hand on what it was given, and Jail Deck would not " +
			"know those jails existed"},
	// "inherit" is the host's own SysV namespace, which is the thing these
	// parameters exist to avoid; it is left out for the same reason
	// allow.sysvipc is.
	"sysvmsg": {kind: paramEnum, values: []string{"disable", "new"}, why: sysvWhy},
	"sysvsem": {kind: paramEnum, values: []string{"disable", "new"}, why: sysvWhy},
	"sysvshm": {kind: paramEnum, values: []string{"disable", "new"}, why: sysvWhy},
	// Both only ever loosen the jail's grip on itself, never the host's.
	"allow.chflags":      {kind: paramBool},
	"allow.set_hostname": {kind: paramBool},
}

const sysvWhy = `"inherit" would be the host's own IPC namespace, which is what "new" exists to avoid`

// managedParams are written by the generated stanza itself. Naming one is a
// different mistake from naming a forbidden one -- usually the right field
// exists and is somewhere else in the spec -- so it gets its own answer.
var managedParams = map[string]string{
	"name":           "it is the spec's name",
	"path":           "it is where the template's clone ends up mounted",
	"host.hostname":  `use "hostname"`,
	"ip4":            `use "network.mode"`,
	"ip6":            `use "network.mode"`,
	"ip4.addr":       `use "network.ip4"`,
	"ip6.addr":       "Jail Deck configures no IPv6 address",
	"interface":      `use "network.interface"`,
	"vnet":           `use "network.mode": "vnet"`,
	"vnet.interface": "the epair is Jail Deck's to allocate (JD-017)",
	"mount.devfs":    "every jail Jail Deck creates mounts devfs",
	"persist":        "a jail Jail Deck creates runs /etc/rc, so it holds itself open",
}

// refusedParams are the ones JD-018 names as never allowed. They would be
// refused anyway for not being on the allowlist; they are here so that the
// refusal says it was a decision rather than an omission.
var refusedParams = map[string]string{
	"allow.mount":       "a jail that can mount can mount over its own confinement",
	"allow.raw_sockets": "a raw socket on the host's network stack can forge and watch the host's own traffic",
	"allow.sysvipc":     `it shares the host's IPC namespaces; "sysvmsg", "sysvsem" and "sysvshm" set to "new" give the jail its own`,
}

// confParam is one rendered line of the stanza: the parameter and the text
// of its value, quoted or not as jail.conf(5) wants it.
type confParam struct {
	Name  string
	Value string
}

// renderParams validates a spec's parameters against the allowlist and
// returns the lines for the stanza, sorted by name so that the same spec
// generates the same file every time.
//
// One function for both jobs: normalize calls it to refuse a bad spec before
// anything is created, and Stanza calls it to write the file. Two would be
// two versions of the allowlist.
func renderParams(params map[string]any) ([]confParam, error) {
	rendered := make([]confParam, 0, len(params))
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rule, allowed := allowedParams[name]
		if !allowed {
			return nil, refuseParam(name)
		}
		value, err := renderParamValue(name, rule, params[name])
		if err != nil {
			return nil, err
		}
		rendered = append(rendered, confParam{Name: name, Value: value})
	}
	return rendered, nil
}

// refuseParam says why a parameter is not written, in the most specific
// terms available: a field that replaces it, a decision that forbids it, or
// the allowlist itself.
func refuseParam(name string) error {
	if instead, managed := managedParams[name]; managed {
		return badSpec(fmt.Sprintf(
			"params.%s is written by Jail Deck itself: %s", name, instead))
	}
	if strings.HasPrefix(name, "exec.") {
		return badSpec(fmt.Sprintf(
			"params.%s is written by Jail Deck itself: the exec hooks start the jail, build its epair "+
				"and apply its limits", name))
	}
	if why, refused := refusedParams[name]; refused {
		return badSpec(fmt.Sprintf("params.%s is never allowed: %s (JD-018)", name, why))
	}
	if strings.HasPrefix(name, "allow.mount") {
		return badSpec(fmt.Sprintf("params.%s is never allowed: %s (JD-018)",
			name, refusedParams["allow.mount"]))
	}
	return badSpec(fmt.Sprintf(
		"params.%s is not a jail parameter Jail Deck will set. The ones it will are %s (JD-018)",
		name, strings.Join(allowedNames(), ", ")))
}

func allowedNames() []string {
	names := make([]string, 0, len(allowedParams))
	for name := range allowedParams {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func renderParamValue(name string, rule paramRule, value any) (string, error) {
	switch rule.kind {
	case paramInt:
		number, ok := asInt(value)
		if !ok {
			return "", badSpec(fmt.Sprintf("params.%s has to be a whole number", name))
		}
		if number < rule.min || number > rule.max {
			return "", badSpec(fmt.Sprintf("params.%s has to be between %d and %d%s",
				name, rule.min, rule.max, becauseOf(rule)))
		}
		return strconv.Itoa(number), nil
	case paramEnum:
		text, ok := value.(string)
		if !ok {
			return "", badSpec(fmt.Sprintf("params.%s has to be one of %s",
				name, quotedList(rule.values)))
		}
		if !slices.Contains(rule.values, text) {
			return "", badSpec(fmt.Sprintf("params.%s cannot be %q; it has to be one of %s%s",
				name, text, quotedList(rule.values), becauseOf(rule)))
		}
		return `"` + text + `"`, nil
	default:
		yes, ok := value.(bool)
		if !ok {
			return "", badSpec(fmt.Sprintf("params.%s has to be true or false", name))
		}
		return strconv.FormatBool(yes), nil
	}
}

func becauseOf(rule paramRule) string {
	if rule.why == "" {
		return ""
	}
	return ": " + rule.why
}

func quotedList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, `"`+value+`"`)
	}
	return strings.Join(quoted, " or ")
}

// asInt accepts the shapes a JSON number arrives in. encoding/json decodes
// one into a float64 when the destination is an any, so a whole number is a
// float that happens to have no fraction -- and 4.5 is a parameter value
// nobody meant.
func asInt(value any) (int, bool) {
	switch number := value.(type) {
	case float64:
		if number != float64(int(number)) {
			return 0, false
		}
		return int(number), true
	case int:
		return number, true
	default:
		return 0, false
	}
}
