package vms

import "github.com/go-chi/chi/v5"

// RegisterVMRoutes mounts the read-only VM views. GET only: there is no write
// path in the port to mount (JD-035).
func RegisterVMRoutes(r chi.Router, h *Handler) {
	r.Get("/vms", h.List)
	r.Get("/vms/{name}", h.Get)
}
