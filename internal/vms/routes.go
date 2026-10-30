package vms

import "github.com/go-chi/chi/v5"

// RegisterVMRoutes mounts the VM views (JD-035), lifecycle (JD-036), and
// creation and destruction (JD-038), the idempotent PUT (JD-041), labels
// (JD-044) and snapshots (JD-045).
func RegisterVMRoutes(r chi.Router, h *Handler) {
	r.Get("/vms", h.List)
	r.Post("/vms", h.Create)
	r.Get("/vms/{name}", h.Get)
	// Its configuration and cloud-init seed as the host has them, read only,
	// a password's hash masked (#127).
	r.Get("/vms/{name}/configuration", h.Configuration)
	r.Put("/vms/{name}", h.Ensure)
	r.Delete("/vms/{name}", h.Destroy)
	r.Post("/vms/{name}/start", h.Start)
	r.Post("/vms/{name}/stop", h.Stop)
	r.Post("/vms/{name}/restart", h.Restart)
	// Labels: ZFS user properties on the guest's dataset, as a jail's are
	// on its own (JD-044).
	r.Patch("/vms/{name}/properties", h.SetProperties)
	// A snapshot of the guest's dataset and its disks, crash-consistent while
	// it runs and said to be (JD-045).
	r.Put("/vms/{name}/snapshots/{snapshot}", h.CreateSnapshot)
}
