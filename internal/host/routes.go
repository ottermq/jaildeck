package host

import "github.com/go-chi/chi/v5"

func RegisterHostRoutes(r chi.Router, h *HostHandler) {
	r.Route("/host", func(r chi.Router) {
		r.Get("/readiness", h.Readiness)
	})
}
