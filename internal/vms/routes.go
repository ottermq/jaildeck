package vms

import "github.com/go-chi/chi/v5"

// RegisterVMRoutes mounts the VM views (JD-035) and lifecycle (JD-036).
func RegisterVMRoutes(r chi.Router, h *Handler) {
	r.Get("/vms", h.List)
	r.Get("/vms/{name}", h.Get)
	r.Post("/vms/{name}/start", h.Start)
	r.Post("/vms/{name}/stop", h.Stop)
	r.Post("/vms/{name}/restart", h.Restart)
}
