package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestRegisterAuthRoutes(t *testing.T) {
	r := chi.NewRouter()
	RegisterAuthRoutes(r, NewAuthHandler(NewAuthService(nil, NewSessionStore())))

	for _, tt := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/auth/login", http.StatusBadRequest}, // empty body
		{http.MethodPost, "/auth/logout", http.StatusNoContent},
		{http.MethodGet, "/auth/me", http.StatusUnauthorized},
		{http.MethodGet, "/auth/login", http.StatusMethodNotAllowed},
		{http.MethodPost, "/auth/me", http.StatusMethodNotAllowed},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
