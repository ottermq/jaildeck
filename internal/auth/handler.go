package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"codeberg.org/Otter-Labs/jaildeck/internal/common"
)

const (
	maxLoginBytes     = 1 << 10
	sessionCookieName = "jaildeck_session"
)

var (
	ErrInvalidRequestBody = common.NewAppError(http.StatusBadRequest, "invalid request body")
)

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

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBytes)

	var req struct {
		Password string `json:"password"`
	}
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

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		h.service.Logout(r.Context(), c.Value)
	}

	http.SetCookie(w, newSessionCookie("", -1))

	w.WriteHeader(http.StatusNoContent)
}

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
			next.ServeHTTP(w, r.WithContext(ctx))
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
		next.ServeHTTP(w, r.WithContext(ctx))
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
		// anyone logged in — deliberately; see #17.
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
