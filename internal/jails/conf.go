package jails

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"text/template"

	"github.com/ottermq/jaildeck/internal/common"
)

// The stanza is generated here, in the domain, and handed to the adapter as
// text (JD-030). What a jail's configuration says is a rule about jails; where
// the file goes is the adapter's business.
//
// text/template rather than string concatenation, because the parts that vary
// are whole lines rather than words and a template shows the finished file.
//
// Comments are '#'. jail.conf(5) also accepts '//' and '/* */', but Jail Deck's
// own parser only strips '#' -- and a file this writes that it cannot read back
// is a file that disappears from the jail listing.
var confTemplate = template.Must(template.New("jail.conf").Parse(
	`# Written by Jail Deck when the jail was created.
#
# Jail Deck does not rewrite this file afterwards, so anything changed here is
# kept. It is read back on every listing: a jail is what the host says it is,
# not what Jail Deck remembers (JD-011).

{{ .Name }} {
{{- if ne .Mode "vnet" }}
	exec.start = "/bin/sh /etc/rc";
{{- end }}
	exec.stop = "/bin/sh /etc/rc.shutdown";
	exec.consolelog = "/var/log/jail_console_${name}.log";
	exec.clean;
	mount.devfs;

	host.hostname = "{{ .Hostname }}";
	path = "{{ .Path }}";
{{- range .Params }}
	{{ .Name }} = {{ .Value }};
{{- end }}

{{- if eq .Mode "alias" }}
	ip4.addr = {{ .IP4 }};
	interface = {{ .Interface }};
	ip6 = disable;
{{- else if eq .Mode "vnet" }}

	vnet;
	vnet.interface = "{{ .JailEpair }}";

	exec.prestart = "ifconfig {{ .Epair }} create";
	exec.prestart += "ifconfig {{ .HostEpair }} descr jail:{{ .Name }} up";
	exec.prestart += "ifconfig {{ .Bridge }} addm {{ .HostEpair }}";
{{- if .Private }}
	exec.prestart += "ifconfig {{ .Bridge }} private {{ .HostEpair }}";
{{- end }}

	exec.start = "ifconfig lo0 inet 127.0.0.1/8 up";
{{- if .IP4 }}
	exec.start += "ifconfig {{ .JailEpair }} inet {{ .IP4 }} up";
{{- end }}
{{- if .Gateway }}
	exec.start += "route -q add default {{ .Gateway }}";
{{- end }}
	exec.start += "/bin/sh /etc/rc";
{{- else }}
	ip4 = {{ .Mode }};
	ip6 = {{ .Mode }};
{{- end }}
{{- if .Poststart }}
{{ range $i, $command := .Poststart }}
	exec.poststart {{ if $i }}+{{ end }}= "{{ $command }}";
{{- end }}
{{- end }}
{{- if .Prestop }}
{{ range $i, $command := .Prestop }}
	exec.prestop {{ if $i }}+{{ end }}= "{{ $command }}";
{{- end }}
{{- end }}
{{- if eq .Mode "vnet" }}

	exec.poststop = "ifconfig {{ .HostEpair }} destroy";
{{- end }}
}
`))

// confData is what the template reads. A struct of its own rather than the
// Spec, so the template cannot reach a field that has not been validated and
// so the jail's path -- which the spec never carries -- has somewhere to go.
type confData struct {
	Name      string
	Hostname  string
	Path      string
	Mode      string
	IP4       string
	Interface string

	// The VNET half. Epair is the pair ("epair7"), and the two ends are
	// derived from it rather than passed separately, so they cannot disagree.
	Epair   string
	Bridge  string
	Private bool
	Gateway string

	// Params are the allowed jail(8) parameters, already rendered and in a
	// fixed order (JD-018).
	Params []confParam
	// Poststart and Prestop are the commands of the two hooks that apply a
	// jail's limits and take them away again, in the order they run. The
	// first line of each hook assigns and the rest append, which the
	// template does from the index rather than from a second field.
	Poststart []string
	Prestop   []string
}

// HostEpair is the end that stays on the host and joins the bridge.
func (d confData) HostEpair() string { return d.Epair + "a" }

// JailEpair is the end moved into the jail's network stack.
func (d confData) JailEpair() string { return d.Epair + "b" }

// Placement is what the stanza needs that the spec does not carry: facts
// decided while the jail was being built rather than asked for by the client.
type Placement struct {
	// Path is where the cloned root ended up mounted.
	Path string
	// Epair is the pair a VNET jail was given, as "epair7" -- the unit,
	// without an end. Empty for every other mode.
	Epair string
}

// Stanza renders a spec's jail.conf block for a jail placed at at.
//
// Exported because the FreeBSD adapter's tests write what this produces and
// read it back with the adapter's own parser: a stanza Jail Deck generates
// and cannot parse is a jail that exists on disk and appears in no listing
// (JD-004), and nothing else in the project would catch that.
//
// It expects a spec that has already been validated; Create normalizes before
// it calls this.
func (spec Spec) Stanza(at Placement) (string, error) {
	if at.Path == "" {
		// Without a path a jail has no root, and jail(8) would refuse the
		// stanza. Caught here because an empty path means the clone reported
		// no mountpoint, which is worth saying plainly.
		return "", common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"the dataset cloned for %q reports no mountpoint, so the jail has no path", spec.Name))
	}
	if spec.Network.Mode == NetworkVNET && at.Epair == "" {
		// Both ends of the stanza are derived from this, so an empty one
		// would generate "ifconfig a create" and a jail with no network.
		return "", common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"no epair was reserved for %q, so its stanza cannot be written", spec.Name))
	}
	params, err := renderParams(spec.Params)
	if err != nil {
		// Unreachable after normalize, which refuses the same specs this
		// does. Returned rather than ignored because Stanza is exported and
		// the adapter's own tests call it directly.
		return "", err
	}
	poststart, prestop := spec.Limits.hooks(spec.Name)

	var out strings.Builder
	data := confData{
		Name:      spec.Name,
		Hostname:  spec.Hostname,
		Path:      at.Path,
		Mode:      string(spec.Network.Mode),
		IP4:       spec.Network.IP4,
		Interface: spec.Network.Interface,
		Epair:     at.Epair,
		Bridge:    spec.Network.Bridge,
		Private:   spec.Network.Private,
		Gateway:   spec.Network.Gateway,
		Params:    params,
		Poststart: poststart,
		Prestop:   prestop,
	}
	if err := confTemplate.Execute(&out, data); err != nil {
		return "", common.NewAppError(http.StatusInternalServerError,
			"could not generate the jail's configuration: "+err.Error())
	}
	return out.String(), nil
}

// --- reading a stanza back ---------------------------------------------
//
// The generator above writes these files, so the domain can read its own
// output back. This is deliberately not a jail.conf(5) parser -- the adapter
// has one of those, for discovering jails (JD-004). This answers one
// narrower question: which settings does this block carry, and with what
// values, so that the stanza a spec would write can be held against the
// stanza that is there (JD-016).

// settingRE matches an assignment: `key = value;` or `key += value;`.
var settingRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*(\+?)=\s*(.*?)\s*;?\s*$`)

// flagRE matches a valueless setting: `exec.clean;`, `mount.devfs;`, and any
// `allow.something;` an operator added by hand.
var flagRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*;\s*$`)

// epairNumberRE finds the unit a stanza already claims.
var epairNumberRE = regexp.MustCompile(`\bepair(\d+)`)

// settingsIn reads a stanza into its settings: for each name, every value
// assigned to it, in order. `=` starts the list and `+=` appends, which is
// exactly how jail(8) reads the two; a flag is a setting with one empty
// value.
//
// Comments, blank lines, the block's own braces and the quoting of a value
// are all dropped, and $name is expanded. None of them is a difference in
// the jail: a stanza an older Jail Deck wrote has its lines in another
// order, jail(8) reads a value the same quoted or not, and a hand-written
// stanza routinely says ${name} where a generated one says the name --
// which is the same jail, because jail(8) expands it before the kernel sees
// it.
//
// Only $name is expanded, which is the variable every stanza uses and the
// only one the adapter's own parser resolves. A value holding any other
// variable is compared as it is written, so it can be reported as a
// difference that is not one -- the safe direction: a false difference is
// read and dismissed, a hidden one is a jail nobody notices has changed.
func settingsIn(content, name string) map[string][]string {
	settings := map[string][]string{}
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(stripComment(line))
		if line == "" || strings.HasSuffix(line, "{") || line == "}" {
			continue
		}
		line = strings.TrimSuffix(line, "}")
		if flag := flagRE.FindStringSubmatch(line); flag != nil {
			settings[flag[1]] = []string{""}
			continue
		}
		matches := settingRE.FindStringSubmatch(line)
		if matches == nil {
			// A form this does not know. Kept under its whole text so it
			// still counts as a difference: silently dropping a line is how
			// a jail that does not match is reported as matching.
			settings[line] = []string{""}
			continue
		}
		setting, appends := matches[1], matches[2] == "+"
		value := expandName(unquote(matches[3]), name)
		if appends {
			settings[setting] = append(settings[setting], value)
		} else {
			settings[setting] = []string{value}
		}
	}
	return settings
}

// stripComment drops a '#' comment, leaving one inside quotes alone -- a
// generated exec line can hold any text at all.
func stripComment(line string) string {
	quoted := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			quoted = !quoted
		case '#':
			if !quoted {
				return line[:i]
			}
		}
	}
	return line
}

// nameVarRE is $name as jail.conf(5) writes it bare, where \b stops it
// matching the start of a longer variable such as $namespace.
var nameVarRE = regexp.MustCompile(`\$name\b`)

// expandName does what jail(8) does with $name before the kernel ever sees
// the value, in the three spellings jail.conf(5) accepts.
func expandName(value, name string) string {
	value = strings.ReplaceAll(value, "${name}", name)
	value = strings.ReplaceAll(value, "$(name)", name)
	return nameVarRE.ReplaceAllString(value, name)
}

func unquote(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return value[1 : len(value)-1]
	}
	return value
}

// epairIn returns the epair unit a stanza claims, as "epair7", or "" when it
// claims none.
//
// A comparison has to regenerate the stanza, and a VNET jail's stanza names
// a unit that was allocated when the jail was created. Allocating a second
// one here would make every VNET jail look as though it had changed -- and
// would be a unit nothing ever uses.
func epairIn(content string) string {
	if match := epairNumberRE.FindStringSubmatch(content); match != nil {
		return "epair" + match[1]
	}
	return ""
}
