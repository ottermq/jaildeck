package jails

import "github.com/go-chi/chi/v5"

func RegisterJailRoutes(r chi.Router, h *JailHandler) {
	r.Route("/jails", func(r chi.Router) {

		r.Get("/", h.List)

		r.Route("/{name}", func(r chi.Router) {
			r.Post("/start", h.Start)
			r.Post("/stop", h.Stop)
			r.Post("/restart", h.Restart)
		})
	})
}
