package jails

import "github.com/go-chi/chi/v5"

func RegisterJailRoutes(r chi.Router, h *JailHandler) {
	r.Route("/jails", func(r chi.Router) {

		r.Get("/", h.List)

		r.Route("/{name}", func(r chi.Router) {
			r.Get("/", h.Get)
			r.Post("/start", h.Start)
			r.Post("/stop", h.Stop)
			r.Post("/restart", h.Restart)

			// The jail's own dataset's user properties, and its children's.
			// PATCH rather than PUT: these merge, and a PUT that replaced the
			// whole set would strip a tag another tool had set.
			r.Patch("/properties", h.SetProperties)
			r.Route("/datasets", func(r chi.Router) {
				r.Put("/{child}", h.EnsureDataset)
				r.Patch("/{child}/properties", h.SetDatasetProperties)
			})
		})
	})
}
