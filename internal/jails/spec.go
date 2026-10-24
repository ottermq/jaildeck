package jails

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// Spec describes a jail to create. It is the body of POST /api/jails, and the
// shape the idempotent PUT will take when it lands (JD-016).
//
// Only what this milestone's first stage builds is here. VNET (JD-017),
// resource limits and the parameter allowlist (JD-018) extend this struct
// rather than replacing it, which is why Network is already a nested object
// with a mode rather than a pair of loose fields.
type Spec struct {
	Name string `json:"name"`
	// Template is the snapshot the jail's root is cloned from, in full:
	// <dataset>@<snapshot>. There is no default, for the reason JD-026 gives
	// for a re-clone — every jail is a clone of some template, so a default
	// would be a choice made silently.
	Template string `json:"template"`
	// Hostname defaults to the jail's name, which is what an operator writing
	// the stanza by hand almost always sets it to.
	Hostname string  `json:"hostname,omitempty"`
	Network  Network `json:"network"`
	// Autostart adds the jail to rc.conf's jail_list, which is what makes it
	// come back after a reboot and nothing else (JD-030).
	Autostart bool `json:"autostart,omitempty"`
	// Start says whether to start the jail once it is configured. A pointer
	// because absent means yes: creating a jail and leaving it down is the
	// unusual request, and it should be the one that has to be spelled out.
	Start *bool `json:"start,omitempty"`
}

// NetworkMode is how a jail reaches the network.
type NetworkMode string

const (
	// NetworkInherit shares the host's addresses and its network stack. The
	// simplest jail, and the one JD-003 warns about: it shares 127.0.0.1 too.
	NetworkInherit NetworkMode = "inherit"
	// NetworkAlias gives the jail one address on a host interface -- the
	// "IP alias" jail of the Handbook, and what the jails on the deployment
	// host use today.
	NetworkAlias NetworkMode = "alias"
	// NetworkDisable gives the jail no network at all.
	NetworkDisable NetworkMode = "disable"
	// NetworkVNET is JD-017. Accepted by the type and refused by the
	// validator until the stanza that builds the epair is written, because
	// answering "unknown mode" to a mode this project has already decided on
	// would be a lie about the roadmap.
	NetworkVNET NetworkMode = "vnet"
)

// Network is the networking half of a spec. The fields each mode reads are
// documented on the mode; a field set for a mode that does not read it is
// refused rather than ignored, because silently dropping an address someone
// asked for is how a jail ends up reachable when it should not be.
type Network struct {
	Mode NetworkMode `json:"mode"`
	// IP4 is the address for NetworkAlias, with its prefix: 192.0.2.10/24.
	IP4 string `json:"ip4,omitempty"`
	// Interface is the host interface the alias is added to.
	Interface string `json:"interface,omitempty"`
}

// The steps of a create, in the order they run. A failure names the one it
// reached, so the response says how far it got (JD-030).
const (
	StepCheck     = "check"
	StepClone     = "clone"
	StepConfigure = "configure"
	StepAutostart = "autostart"
	StepStart     = "start"
)

// Created is what creating a jail did, or got as far as doing. It is returned
// filled in on failure as well as on success: a half-created jail is reported
// rather than rolled back (JD-030), and this is the report.
type Created struct {
	Jail Jail `json:"jail"`
	// Root is the jail's own dataset and Mountpoint is where it is mounted,
	// which is the jail's path.
	Root       string `json:"root"`
	Mountpoint string `json:"mountpoint"`
	// From is the template snapshot the root was cloned from.
	From string `json:"from"`
	// Config is the file the stanza was written to.
	Config    string `json:"config,omitempty"`
	Autostart bool   `json:"autostart"`
	Running   bool   `json:"running"`
	// FailedAt names the step that stopped, and is absent on success.
	FailedAt string `json:"failed_at,omitempty"`
	// Error is why it stopped. The body of a failed create carries both: what
	// exists is the recovery information, and this is the reason it is
	// needed. Filled in by the handler, which is where the response is
	// decided -- the service returns a Go error like every other method here.
	Error string `json:"error,omitempty"`
}

// starts reports whether the jail should be started, absent meaning yes.
func (s Spec) starts() bool {
	return s.Start == nil || *s.Start
}

// normalize validates a spec and fills in its defaults, returning the spec
// that will actually be built. Every refusal names the field.
//
// Validation that the host would do is left to the host (JD-012): whether the
// interface exists, whether the address is already taken, whether the template
// snapshot is there. Only what this domain alone can judge is checked here.
func (s Spec) normalize() (Spec, error) {
	name, err := newCreatedJailName(s.Name)
	if err != nil {
		return Spec{}, err
	}
	s.Name = name

	if s.Template == "" {
		return Spec{}, badSpec("template is required: it is the dataset@snapshot the jail's root is cloned from")
	}
	dataset, snapshot, ok := strings.Cut(s.Template, "@")
	if !ok || dataset == "" || snapshot == "" {
		return Spec{}, badSpec(fmt.Sprintf(
			"%q is not a snapshot name; it has to be dataset@snapshot", s.Template))
	}

	if s.Hostname == "" {
		s.Hostname = s.Name
	}

	network, err := s.Network.normalize()
	if err != nil {
		return Spec{}, err
	}
	s.Network = network
	return s, nil
}

func (n Network) normalize() (Network, error) {
	switch n.Mode {
	case "":
		// A spec that says nothing about networking gets the jail that is
		// hardest to get wrong, not the one that is most convenient.
		n.Mode = NetworkDisable
	case NetworkInherit, NetworkDisable:
	case NetworkAlias:
		if n.IP4 == "" {
			return Network{}, badSpec(`network.ip4 is required for mode "alias"`)
		}
		if n.Interface == "" {
			return Network{}, badSpec(`network.interface is required for mode "alias": an alias is added to an interface of the host`)
		}
	case NetworkVNET:
		return Network{}, common.NewAppError(http.StatusNotImplemented,
			`network mode "vnet" is decided (JD-017) but not built yet; use "alias", "inherit" or "disable"`)
	default:
		return Network{}, badSpec(fmt.Sprintf(
			`unknown network mode %q; it has to be "alias", "inherit", "disable" or "vnet"`, n.Mode))
	}

	if n.Mode != NetworkAlias {
		if n.IP4 != "" {
			return Network{}, badSpec(fmt.Sprintf(`network.ip4 is only read for mode "alias", and this spec asks for %q`, n.Mode))
		}
		if n.Interface != "" {
			return Network{}, badSpec(fmt.Sprintf(`network.interface is only read for mode "alias", and this spec asks for %q`, n.Mode))
		}
	}
	return n, nil
}

// newCreatedJailName is stricter than NewJailName, and deliberately so.
//
// NewJailName validates a name that already exists somewhere -- in jls or in a
// config file -- and has to accept whatever the host reports. This one decides
// what Jail Deck is willing to create, and a name has to be three things at
// once: a jail(8) name, a jail.conf block name, and a ZFS dataset component.
//
// A dot is the one character that passes all three and still causes trouble.
// jail(8) reads a dotted name as a hierarchical jail, and rc.d/jail maps both
// '.' and '/' to '_' when it builds its per-jail variable names, so two jails
// whose names differ only by a dot collide there. A jail already called that
// is still listed and still operable; one is simply not created here.
func newCreatedJailName(name string) (string, error) {
	if name == "" {
		return "", badSpec("name is required")
	}
	if len(name) > maxJailNameLength {
		return "", badSpec(fmt.Sprintf(
			"name %q is longer than %d characters", name, maxJailNameLength))
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case (r == '-' || r == '_') && i > 0:
		default:
			return "", badSpec(fmt.Sprintf(
				"name %q cannot be created: a name may hold letters, digits, '-' and '_', and has to begin with a letter or a digit", name))
		}
	}
	return name, nil
}

// maxJailNameLength keeps the generated hostname inside what a FreeBSD
// hostname allows, which is the shortest of the three limits a name meets.
const maxJailNameLength = 63

func badSpec(message string) error {
	return common.NewAppError(http.StatusBadRequest, message)
}
