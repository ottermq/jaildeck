package host

import "github.com/go-chi/chi/v5"

// RegisterHostRoutes mounts the host routes. extra are more reports on the
// host from domains of their own: what is reclaimable (#125).
func RegisterHostRoutes(r chi.Router, h *HostHandler, extra ...func(chi.Router)) {
	r.Route("/host", func(r chi.Router) {
		r.Get("/readiness", h.Readiness)
		r.Get("/layout", h.Layout)
		r.Put("/layout", h.EnsureLayout)
		for _, mount := range extra {
			mount(r)
		}
	})
}
