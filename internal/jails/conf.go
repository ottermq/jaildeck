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
	exec.start = "/bin/sh /etc/rc";
	exec.stop = "/bin/sh /etc/rc.shutdown";
	exec.consolelog = "/var/log/jail_console_${name}.log";
	exec.clean;
	mount.devfs;

	host.hostname = "{{ .Hostname }}";
	path = "{{ .Path }}";

{{- if eq .Mode "alias" }}
	ip4.addr = {{ .IP4 }};
	interface = {{ .Interface }};
	ip6 = disable;
{{- else }}
	ip4 = {{ .Mode }};
	ip6 = {{ .Mode }};
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
}

// Stanza renders a spec's jail.conf block, for a jail whose root is mounted
// at path.
//
// Exported because the FreeBSD adapter's tests write what this produces and
// read it back with the adapter's own parser: a stanza Jail Deck generates
// and cannot parse is a jail that exists on disk and appears in no listing
// (JD-004), and nothing else in the project would catch that.
//
// It expects a spec that has already been validated; Create normalizes before
// it calls this.
func (spec Spec) Stanza(path string) (string, error) {
	if path == "" {
		// Without a path a jail has no root, and jail(8) would refuse the
		// stanza. Caught here because an empty path means the clone reported
		// no mountpoint, which is worth saying plainly.
		return "", common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"the dataset cloned for %q reports no mountpoint, so the jail has no path", spec.Name))
	}
	var out strings.Builder
	data := confData{
		Name:      spec.Name,
		Hostname:  spec.Hostname,
		Path:      path,
		Mode:      string(spec.Network.Mode),
		IP4:       spec.Network.IP4,
		Interface: spec.Network.Interface,
	}
	if err := confTemplate.Execute(&out, data); err != nil {
		return "", common.NewAppError(http.StatusInternalServerError,
			"could not generate the jail's configuration: "+err.Error())
	}
	return out.String(), nil
}
