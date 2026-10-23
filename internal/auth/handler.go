package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/ottermq/jaildeck/internal/common"
)

const (
	maxLoginBytes     = 1 << 10
	sessionCookieName = "jaildeck_session"
)

var (
	ErrInvalidRequestBody = common.NewAppError(http.StatusBadRequest, "invalid request body")
)

// logEntryEnricher is chi's request log entry, narrowed to the one method
// RequireAuth needs. Declared here rather than imported so internal/auth does
// not depend on internal/app, which already imports it.
type logEntryEnricher interface {
	AddAttrs(args ...any)
}

// LoginRequest is the body of POST /api/auth/login.
type LoginRequest struct {
	Password string `json:"password"`
}

type LoginResponse struct {
	Authenticated bool `json:"authenticated"`
}

type MeResponse struct {
	Principal Principal `json:"principal"`
}

type Principal struct {
	Kind      PrincipalKind `json:"kind"`
	Name      string        `json:"name"`
	ExpiresAt *time.Time    `json:"expires_at,omitempty"`
}

type PrincipalKind string //session | token
const (
	kindSession PrincipalKind = "session"
	kindToken   PrincipalKind = "token"
)

type AuthHandler struct {
	service *AuthService
}

func NewAuthHandler(service *AuthService) *AuthHandler {
	return &AuthHandler{
		service: service,
	}
}

// Login exchanges the operator password for a session cookie.
//
// @Summary      Sign in
// @Description  Sets an HttpOnly, Secure, SameSite=Strict session cookie. Secure means a
// @Description  browser discards it over plain HTTP on a non-loopback origin, so reach a
// @Description  remote host through an SSH forward rather than binding it to 0.0.0.0 (JD-002).
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        credentials  body      LoginRequest  true  "The operator password"
// @Success      200          {object}  LoginResponse
// @Failure      400          {object}  common.ErrorResponse  "Malformed or oversized body"
// @Failure      401          {object}  common.ErrorResponse  "Wrong password"
// @Router       /auth/login [post]
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBytes)

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			common.HandlerError(w, common.ErrRequestTooLarge)
			return
		}
		common.HandlerError(w, ErrInvalidRequestBody)
		return
	}
	token, err := h.service.Login(r.Context(), req.Password)
	if err != nil {
		common.HandlerError(w, err)
		return
	}

	http.SetCookie(w, newSessionCookie(token, int(h.service.SessionMaxAge().Seconds())))
	common.WriteJSON(w, http.StatusOK, LoginResponse{Authenticated: true})

}

// Logout ends the session the cookie names.
//
// @Summary      Sign out
// @Description  Clears the cookie and forgets the session server-side. Harmless without
// @Description  one, so a client that has already lost its cookie can still call it.
// @Tags         auth
// @Success      204  "No content"
// @Router       /auth/logout [post]
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		h.service.Logout(r.Context(), c.Value)
	}

	http.SetCookie(w, newSessionCookie("", -1))

	w.WriteHeader(http.StatusNoContent)
}

// Me reports who the request is authenticated as.
//
// @Summary      The current principal
// @Description  Says whether this request arrived as a session or a token, and when a
// @Description  session expires. Sessions live in memory, so restarting the service signs
// @Description  every browser out; tokens are stored and survive.
// @Tags         auth
// @Produce      json
// @Success      200  {object}  MeResponse
// @Failure      401  {object}  common.ErrorResponse  "Neither a session nor a token"
// @Security     BearerAuth
// @Router       /auth/me [get]
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	session, ok := SessionFromContext(r.Context())
	if ok {
		common.WriteJSON(w, http.StatusOK, MeResponse{
			Principal{
				Kind:      kindSession,
				Name:      "",
				ExpiresAt: &session.ExpiresAt,
			},
		})
		return
	}
	name, ok := TokenNameFromContext(r.Context())
	if ok {
		common.WriteJSON(w, http.StatusOK, MeResponse{
			Principal{
				Kind:      kindToken,
				Name:      name,
				ExpiresAt: nil,
			},
		})
		return
	}
	// The middleware guarantees this;
	// reaching it means /auth/me was registered without RequireAuth.
	common.HandlerError(w, common.NewAppError(http.StatusInternalServerError, "internal server error"))
}

/* Middleware */

type sessionKey struct{}
type bearerKey struct{}

func (h *AuthHandler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := bearerToken(r)
		// A bad token deliberately returns 401, rather than falling back to a
		// valid cookie.
		if ok {
			token, err := h.service.AuthenticateToken(r.Context(), presented)
			if err != nil {
				common.HandlerError(w, err)
				return
			}
			ctx := context.WithValue(r.Context(), bearerKey{}, token.Name)
			next.ServeHTTP(w, withPrincipalLogged(r.WithContext(ctx), kindToken, token.Name))
			return
		}
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			common.HandlerError(w, ErrUnauthenticated)
			return
		}
		session, err := h.service.Authenticate(r.Context(), c.Value)
		if err != nil {
			common.HandlerError(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, session)
		next.ServeHTTP(w, withPrincipalLogged(r.WithContext(ctx), kindSession, ""))
	})
}

func SessionFromContext(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionKey{}).(Session)
	return session, ok
}

func TokenNameFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(bearerKey{}).(string)
	return name, ok
}

func newSessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge, // -1 = delete, 0 = omit field
		HttpOnly: true,
		// Browsers treat localhost as a secure context, so loopback development works
		// over plain HTTP (JD-003). A non-loopback plain-HTTP deployment will not keep
		// anyone logged in — deliberately; see #27.
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")

	scheme, token, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}

	return token, true
}

// withPrincipalLogged records the authenticated principal on poth log sink and
// returns the request the handler should receive.
//
// Both are needed and neither can server the other: chi's entry is written after
// the handler returns and is reached throught the request as it stands here,
// while the context logger travels forward only on the request this returns. An
// attribute added to one is invisible to the other.
func withPrincipalLogged(r *http.Request, kind PrincipalKind, name string) *http.Request {
	attrs := []any{"principal_kind", string(kind)}
	// A session has no name - there is one operator - so the kind is the whole
	// story. A token's name is what `jaildeck token revoke` takes.
	if name != "" {
		attrs = append(attrs, "principal", name)
	}
	if entry, ok := middleware.GetLogEntry(r).(logEntryEnricher); ok {
		entry.AddAttrs(attrs...)
	}
	l := common.LoggerFromContext(r.Context()).With(attrs...)
	return r.WithContext(common.ContextWithLogger(r.Context(), l))
}
