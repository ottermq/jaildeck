package audit

import "github.com/go-chi/chi/v5"

func RegisterAuditRoutes(r chi.Router, h *AuditHandler) {
	r.Route("/audit", func(r chi.Router) {
		r.Get("/", h.List)
	})
}
