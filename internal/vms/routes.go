package vms

import "github.com/go-chi/chi/v5"

// RegisterVMRoutes mounts the VM views (JD-035), lifecycle (JD-036), and
// creation and destruction (JD-038).
func RegisterVMRoutes(r chi.Router, h *Handler) {
	r.Get("/vms", h.List)
	r.Post("/vms", h.Create)
	r.Get("/vms/{name}", h.Get)
	r.Delete("/vms/{name}", h.Destroy)
	r.Post("/vms/{name}/start", h.Start)
	r.Post("/vms/{name}/stop", h.Stop)
	r.Post("/vms/{name}/restart", h.Restart)
}
