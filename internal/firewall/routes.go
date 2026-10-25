package firewall

import "github.com/go-chi/chi/v5"

// RegisterFirewallRoutes mounts the read-only firewall views. GET only: there
// is no write path in the port to mount (JD-020, JD-034).
func RegisterFirewallRoutes(r chi.Router, h *Handler) {
	r.Get("/firewall", h.Report)
	r.Get("/firewall/tables/{name}", h.Table)
	r.Get("/firewall/log", h.Log)
}
