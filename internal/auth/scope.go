package auth

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/state"
)

// Scope is something a token may do beyond the plain API (#178, JD-046).
type Scope string

// ScopeConsole opens a shell in a jail or a guest's serial console: root,
// which a platform with tenants hands to its own token only (#155).
const ScopeConsole Scope = "console"

// Scopes are every scope there is, in the order they are listed.
var Scopes = []Scope{ScopeConsole}

// ParseScopes reads scope names as given to `jaildeck token add --scope`,
// and refuses one that does not exist rather than storing a typo that
// grants nothing.
func ParseScopes(names []string) ([]string, error) {
	scopes := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !slices.Contains(Scopes, Scope(name)) {
			return nil, fmt.Errorf("%q is not a scope; the scopes are %s", name, scopeList())
		}
		if !slices.Contains(scopes, name) {
			scopes = append(scopes, name)
		}
	}
	return scopes, nil
}

func scopeList() string {
	names := make([]string, len(Scopes))
	for i, scope := range Scopes {
		names[i] = string(scope)
	}
	return strings.Join(names, ", ")
}

// TokenHas answers whether a token may use a scope. A token made before
// scopes existed may use them all.
func TokenHas(token state.Token, scope Scope) bool {
	return token.Scopes == nil || slices.Contains(token.Scopes, string(scope))
}

// TokenScopes is a token's scopes spelled out, the ones it has from before
// scopes existed included.
func TokenScopes(token state.Token) []Scope {
	granted := make([]Scope, 0, len(Scopes))
	for _, scope := range Scopes {
		if TokenHas(token, scope) {
			granted = append(granted, scope)
		}
	}
	return granted
}

// sessionScopes are the operator's login's: every scope, less those the
// host has turned off for the browser.
func (h *AuthHandler) sessionScopes() []Scope {
	granted := make([]Scope, 0, len(Scopes))
	for _, scope := range Scopes {
		if !slices.Contains(h.sessionWithout, scope) {
			granted = append(granted, scope)
		}
	}
	return granted
}

// SetSessionWithout takes scopes away from the browser session: the console,
// on a host whose operator has set JAILDECK_CONSOLE_UI=off (#155). Tokens
// keep theirs.
func (h *AuthHandler) SetSessionWithout(scopes ...Scope) { h.sessionWithout = scopes }

// Allow refuses a request whose principal lacks scope, with 403 and why. It
// is for a handler to call, not a middleware, because the console decides
// before upgrading and a refusal must come back as plain HTTP.
func (h *AuthHandler) Allow(r *http.Request, scope Scope) error {
	if token, ok := tokenFromContext(r.Context()); ok {
		if TokenHas(token, scope) {
			return nil
		}
		return common.NewAppError(http.StatusForbidden, fmt.Sprintf(
			"the token %q does not have the %s scope; make one that does with `jaildeck token add <name> --scope %s`",
			token.Name, scope, scope))
	}
	if _, ok := SessionFromContext(r.Context()); ok {
		if slices.Contains(h.sessionScopes(), scope) {
			return nil
		}
		return common.NewAppError(http.StatusForbidden, fmt.Sprintf(
			"the %s is turned off for the browser on this host (JAILDECK_CONSOLE_UI=off); an API token with the %s scope can still open one",
			scope, scope))
	}
	return ErrUnauthenticated
}
