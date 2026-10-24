package jails

import (
	"fmt"
	"net/http"
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
