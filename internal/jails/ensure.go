package jails

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// Ensured is what PUT /api/jails/{name} did, or found already done.
//
// It embeds Created because the three answers are one shape seen from three
// distances: a jail that had to be made is reported exactly as POST reports
// it, a jail that already matched is reported as the host describes it, and
// a jail that differs is both of those plus the differences.
type Ensured struct {
	Created
	// Changed says whether this call changed anything on the host. It is
	// false for a jail that already matched and false for one that differs:
	// nothing here reconciles a jail towards a spec (JD-016).
	Changed bool `json:"changed"`
	// Differences is every setting the spec and the host disagree about,
	// and is the whole of the answer when they do.
	Differences []Difference `json:"differences,omitempty"`
}

// Difference is one thing the spec asks for and the host does not have.
//
// Field is a jail.conf(5) setting for everything the stanza carries, because
// that is where the difference actually is and what an operator would edit.
// The two that live elsewhere are named for the spec field instead:
// "template" is the root's clone origin and "autostart" is rc.conf's
// jail_list.
type Difference struct {
	Field string `json:"field"`
	Want  string `json:"want"`
	Have  string `json:"have"`
}

// Ensure makes PUT /api/jails/{name} idempotent: create the jail if it is
// absent, say so if it is already what the spec describes, and refuse with
// the differences if it is not (JD-016).
//
// **It never reconciles.** A jail that differs is left exactly as it is, and
// the client decides what to do -- which is the whole position JD-016 takes:
// the desired state lives in the client, and Jail Deck is the executor that
// reports truthfully.
func (s *JailService) Ensure(ctx context.Context, name JailName, spec Spec) (Ensured, error) {
	switch {
	case spec.Name == "":
		// The URL has already named it, so the body need not repeat it.
		spec.Name = name.String()
	case spec.Name != name.String():
		return Ensured{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"the spec is for a jail named %q and the URL names %q; a jail has one name",
			spec.Name, name))
	}
	spec, err := spec.normalize()
	if err != nil {
		return Ensured{}, err
	}

	jail, err := s.Get(ctx, name)
	switch {
	case errors.Is(err, ErrJailNotFound):
		created, createErr := s.Create(ctx, spec)
		// Changed on anything that exists now, not on success: a create that
		// stopped at the start left a dataset and a configuration file
		// behind, and a client told "nothing changed" would be misled about
		// the one case where it most matters (JD-030).
		return Ensured{Created: created, Changed: created.Root != ""}, createErr
	case err != nil:
		return Ensured{}, err
	}
	return s.compare(ctx, jail, spec)
}

// compare answers whether the jail that is there is the jail the spec
// describes, by rendering the stanza this spec would write and holding it
// against the stanza on disk.
//
// Nothing here is a list of fields to check. A parameter added to the
// allowlist is compared the day the generator starts writing it, because the
// comparison is over whatever the two files say rather than over what this
// function knows to ask about.
func (s *JailService) compare(ctx context.Context, jail Jail, spec Spec) (Ensured, error) {
	if s.configReader == nil || s.cloneOrigin == nil || s.autostartSource == nil {
		return Ensured{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck cannot read a jail back, so it cannot tell whether one already matches the spec")
	}
	result := Ensured{Created: Created{
		Jail:       jail,
		Mountpoint: jail.Path,
		Running:    jail.Status == JailStatusRunning,
	}}

	config, exists, err := s.configReader.ConfigFile(ctx, jail.Name)
	if err != nil {
		return result, err
	}
	if !exists {
		// The same refusal a destroy makes, for the same reason: the jail's
		// block is in /etc/jail.conf, which belongs to the operator and
		// which Jail Deck never writes (JD-030). There is no file to compare
		// and there would be none to change.
		return result, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is not there, so %s is configured somewhere Jail Deck does not write -- most likely "+
				"/etc/jail.conf, which belongs to you. Jail Deck can neither compare that jail "+
				"against a spec nor change it", config, jail.Name))
	}
	result.Config = config

	if jail.Path == "" {
		return result, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is configured with no path, so it has no root to compare a template against", jail.Name))
	}
	content, err := s.configReader.ReadConfig(ctx, jail.Name)
	if err != nil {
		return result, err
	}
	root, origin, err := s.cloneOrigin.RootOrigin(ctx, jail.Path)
	if err != nil {
		return result, err
	}
	result.Root, result.From = root, origin

	autostarts, err := s.autostartSource.Autostarts(ctx, jail.Name)
	if err != nil {
		return result, common.NewAppError(http.StatusInternalServerError,
			"could not read whether "+jail.Name+" is in jail_list: "+err.Error())
	}
	result.Autostart = autostarts

	// The jail's path as the host reports it, and the epair the existing
	// stanza already claims. Allocating a fresh unit here would make every
	// VNET jail look changed, over a unit nothing would ever use -- and the
	// path has to be the host's, so that a stanza naming a different one
	// shows up as the difference it is.
	want, err := spec.Stanza(Placement{Path: jail.Path, Epair: epairIn(content)})
	if err != nil {
		return result, err
	}

	differences := make([]Difference, 0)
	if spec.Template != origin {
		differences = append(differences, Difference{
			Field: "template", Want: spec.Template, Have: origin})
	}
	differences = append(differences, diffStanzas(want, content, jail.Name)...)
	if spec.Autostart != autostarts {
		differences = append(differences, Difference{
			Field: "autostart",
			Want:  strconv.FormatBool(spec.Autostart),
			Have:  strconv.FormatBool(autostarts),
		})
	}
	if len(differences) > 0 {
		result.Differences = differences
	}
	return result, nil
}

// diffStanzas names every setting two stanzas disagree about, in the order
// the settings sort.
//
// Settings rather than bytes. A stanza an older Jail Deck wrote has its
// lines in a different order, and that is not a difference in the jail;
// every value is still compared, including one belonging to a setting only
// one side has.
func diffStanzas(want, have, name string) []Difference {
	wanted, present := settingsIn(want, name), settingsIn(have, name)

	names := make([]string, 0, len(wanted)+len(present))
	for name := range wanted {
		names = append(names, name)
	}
	for name := range present {
		if _, both := wanted[name]; !both {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	differences := make([]Difference, 0)
	for _, name := range names {
		wantValues, asked := wanted[name]
		haveValues, has := present[name]
		// A setting that is there always has at least one value -- a flag's
		// is the empty string -- so an absent one's nil slice can never
		// equal a present one's, and this one comparison answers both
		// "different value" and "only one side has it".
		if slices.Equal(wantValues, haveValues) {
			continue
		}
		differences = append(differences, Difference{
			Field: name,
			Want:  showSetting(wantValues, asked),
			Have:  showSetting(haveValues, has),
		})
	}
	return differences
}

// showSetting renders a setting's values for the diff. An absent setting is
// empty and a flag is "set", so that a flag one side alone carries does not
// read as two empty strings.
func showSetting(values []string, present bool) string {
	switch {
	case !present:
		return ""
	case len(values) == 1 && values[0] == "":
		return "set"
	default:
		return strings.Join(values, "; ")
	}
}
