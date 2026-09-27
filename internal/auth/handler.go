package auth

import (
	"encoding/json"
	"errors"
	"net/http"
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
	ExpiresAt time.Time `json:"expires_at"`
}

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

	common.WriteJSON(w, http.StatusOK, MeResponse{ExpiresAt: session.ExpiresAt})
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
