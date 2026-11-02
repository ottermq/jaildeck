package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/state"
)

// Jail Deck runs as two processes (JD-056): an unprivileged front that
// authenticates, and the root side that does the work. The front checks the
// password, the session or the token, then names the principal it found in
// one header on the request it passes on. The root side reads only that.
//
// The header is trusted because of where it arrives: the root side's socket,
// which only the front can open. A front that is taken over can name any
// principal, which is the same as holding a token with the whole API -- and
// that is what JD-056 says the split does not protect.

// PrincipalHeader carries the principal from the front to the root side.
const PrincipalHeader = "X-Jaildeck-Principal"

// forwarded is the principal as the header carries it: the token without its
// hash, or the session without its fingerprint.
type forwarded struct {
	Kind PrincipalKind `json:"kind"`
	Name string        `json:"name,omitempty"`
	// Scopes keeps nil and empty apart, as state.Token does: nil is a token
	// made before scopes existed, which has every one.
	Scopes  []string  `json:"scopes"`
	Allow   []string  `json:"allow,omitempty"`
	Expires time.Time `json:"expires,omitzero"`
}

// StripForwarded removes any principal a request arrived with, so that only
// the front's own reaches the root side.
func StripForwarded(h http.Header) {
	for name := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "X-Jaildeck-") {
			h.Del(name)
		}
	}
}

// ForwardPrincipal names the principal RequireAuth put in ctx on the request
// the front passes on. It strips what was there first, and names nothing for
// a request RequireAuth did not see.
func ForwardPrincipal(ctx context.Context, h http.Header) {
	StripForwarded(h)
	var p forwarded
	if token, ok := tokenFromContext(ctx); ok {
		p = forwarded{Kind: kindToken, Name: token.Name, Scopes: token.Scopes, Allow: token.Allow}
	} else if session, ok := SessionFromContext(ctx); ok {
		p = forwarded{Kind: kindSession, Expires: session.ExpiresAt}
	} else {
		return
	}
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	// Base64, so that nothing in a token's name or a grant's value can end
	// the header or start another.
	h.Set(PrincipalHeader, base64.RawURLEncoding.EncodeToString(data))
}

func readForwarded(value string) (forwarded, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return forwarded{}, err
	}
	var p forwarded
	if err := json.Unmarshal(data, &p); err != nil {
		return forwarded{}, err
	}
	switch {
	case p.Kind == kindToken && p.Name != "":
	case p.Kind == kindSession:
	default:
		return forwarded{}, fmt.Errorf("no principal kind %q", p.Kind)
	}
	return p, nil
}

// Trusted is the root side's RequireAuth: it takes the principal the front
// named and puts it where RequireAuth would have, so Restrict, Allow and
// PrincipalOf work unchanged. A request without one is refused, as if it
// had come with no credentials.
func (h *AuthHandler) Trusted(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := readForwarded(r.Header.Get(PrincipalHeader))
		if err != nil {
			common.HandlerError(w, ErrUnauthenticated)
			return
		}
		ctx := r.Context()
		if p.Kind == kindToken {
			ctx = context.WithValue(ctx, bearerKey{}, state.Token{Name: p.Name, Scopes: p.Scopes, Allow: p.Allow})
		} else {
			ctx = context.WithValue(ctx, sessionKey{}, Session{ExpiresAt: p.Expires})
		}
		next.ServeHTTP(w, withPrincipalLogged(r.WithContext(ctx), p.Kind, p.Name))
	})
}

// RecheckHandler answers, on the front's own socket, whether the credentials
// a request came with still hold: 204 or 401. The root side asks it for a
// console or an event stream that outlives its request (JD-042), since the
// sessions and the tokens are the front's.
func (h *AuthHandler) RecheckHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h.Recheck(r); err != nil {
			common.HandlerError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// RemoteRecheck is Recheck for the root side: it sends the request's own
// credentials to the front's RecheckHandler through client. Anything but a
// 204 is a no, including a front that does not answer.
func RemoteRecheck(client *http.Client) func(*http.Request) error {
	return func(r *http.Request) error {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://front/recheck", nil)
		if err != nil {
			return err
		}
		for _, name := range []string{"Authorization", "Cookie"} {
			if value := r.Header.Values(name); len(value) > 0 {
				req.Header[name] = value
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("asking the front whether the login holds: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return ErrUnauthenticated
		}
		return nil
	}
}
