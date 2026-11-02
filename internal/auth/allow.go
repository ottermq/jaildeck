package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/state"
)

// A restricted token can do only what its grants name, where any other token
// can do the whole plain API (JD-055). Scopes add to the plain API; grants
// take it away. A platform's worker gets a token that reads and runs commands
// in its own jails, so that a leaked token no longer reaches the rest of the
// host.
//
// The grants:
//
//	read                     any GET: the lists, the details, the metrics, the stream
//	exec:<property>=<value>  POST /api/jails/{name}/exec, in a jail whose
//	                         ZFS user property has that value
//
// Everything else is refused with 403, so what a grant does not name is
// never reached, including routes added after the token was made.

const grantRead = "read"

// grant is one parsed grant: read, or exec where a property has a value.
type grant struct {
	read     bool
	property string
	value    string
}

func parseGrant(text string) (grant, error) {
	text = strings.TrimSpace(text)
	if text == grantRead {
		return grant{read: true}, nil
	}
	selector, ok := strings.CutPrefix(text, "exec:")
	if !ok {
		return grant{}, fmt.Errorf("%q is not a grant; the grants are read and exec:<property>=<value>", text)
	}
	property, value, ok := strings.Cut(selector, "=")
	// A ZFS user property has a colon in its name (zfsprops(7)), which is
	// also what keeps it apart from zfs's own properties.
	if !ok || !strings.Contains(property, ":") || value == "" ||
		strings.ContainsFunc(selector, func(r rune) bool { return r < ' ' }) {
		return grant{}, fmt.Errorf("%q is not a grant: exec takes a user property and its value, as exec:otter:role=cluster", text)
	}
	return grant{property: property, value: value}, nil
}

// ParseGrants reads grants as given to `jaildeck token add --allow`, and
// refuses one it does not understand rather than storing a typo.
func ParseGrants(texts []string) ([]string, error) {
	out := []string{}
	for _, text := range texts {
		if _, err := parseGrant(text); err != nil {
			return nil, err
		}
		if text = strings.TrimSpace(text); !slices.Contains(out, text) {
			out = append(out, text)
		}
	}
	return out, nil
}

// Restricted answers whether a token is limited to its grants.
func Restricted(token state.Token) bool { return len(token.Allow) > 0 }

// DescribeGrants is a token's grants in words, for `token list` and the
// refusal.
func DescribeGrants(allow []string) string {
	parts := make([]string, 0, len(allow))
	for _, text := range allow {
		g, err := parseGrant(text)
		switch {
		case err != nil:
			parts = append(parts, text)
		case g.read:
			parts = append(parts, "read")
		default:
			parts = append(parts, "exec in jails with "+g.property+"="+g.value)
		}
	}
	return strings.Join(parts, "; ")
}

// JailProperties reads a jail's ZFS user properties, for an exec grant to
// match against. It is the app's to supply: auth does not know jails.
type JailProperties func(ctx context.Context, jail string) (map[string]string, error)

// Restrict refuses what a restricted token's grants do not name. It goes
// after RequireAuth, on every authenticated route; for a session or a token
// without grants it does nothing.
func (h *AuthHandler) Restrict(properties JailProperties) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := tokenFromContext(r.Context())
			if !ok || !Restricted(token) {
				next.ServeHTTP(w, r)
				return
			}
			if err := allowed(r, token, properties); err != nil {
				common.HandlerError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func allowed(r *http.Request, token state.Token, properties JailProperties) error {
	grants := make([]grant, 0, len(token.Allow))
	for _, text := range token.Allow {
		g, err := parseGrant(text)
		if err != nil {
			// A grant this build does not read grants nothing.
			continue
		}
		grants = append(grants, g)
	}
	refuse := func(why string) error {
		return common.NewAppError(http.StatusForbidden, fmt.Sprintf(
			"the token %q may only %s; %s", token.Name, DescribeGrants(token.Allow), why))
	}

	if r.Method == http.MethodGet {
		if slices.ContainsFunc(grants, func(g grant) bool { return g.read }) {
			return nil
		}
		return refuse("it has no read grant")
	}

	jail, ok := execTarget(r)
	if !ok {
		return refuse(r.Method + " " + r.URL.Path + " is neither")
	}
	var execs []grant
	for _, g := range grants {
		if !g.read {
			execs = append(execs, g)
		}
	}
	if len(execs) == 0 {
		return refuse("it has no exec grant")
	}
	props, err := properties(r.Context(), jail)
	if err != nil {
		// Not found is a 404 whoever asks; anything else, refuse rather
		// than run a command in a jail whose properties were not read.
		var appErr *common.AppError
		if errors.As(err, &appErr) && appErr.Code == http.StatusNotFound {
			return err
		}
		return refuse("the jail's properties could not be read: " + err.Error())
	}
	for _, g := range execs {
		if props[g.property] == g.value {
			return nil
		}
	}
	return refuse("the jail " + jail + " has no matching property")
}

// execTarget is the jail an exec request names, from its path. A path with
// escapes in it is not read: the router matches the escaped form and this
// would read the unescaped one, and two readings of one path is how a check
// and the handler come to disagree.
func execTarget(r *http.Request) (string, bool) {
	if r.Method != http.MethodPost || r.URL.RawPath != "" {
		return "", false
	}
	parts := strings.Split(r.URL.Path, "/")
	// "", "api", "jails", "<name>", "exec"
	if len(parts) != 5 || parts[0] != "" || parts[1] != "api" || parts[2] != "jails" || parts[4] != "exec" || parts[3] == "" {
		return "", false
	}
	return parts[3], true
}
