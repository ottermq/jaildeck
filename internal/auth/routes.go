package auth

import "github.com/go-chi/chi/v5"

func RegisterAuthRoutes(r chi.Router, h *AuthHandler) {
	r.Route("/auth", func(r chi.Router) {
		r.Post("/login", h.Login)
		r.Post("/logout", h.Logout)
		r.Get("/me", h.Me)
	})
}
