package metrics

import "github.com/go-chi/chi/v5"

// RegisterMetricsRoutes mounts the report.
//
// At /api/metrics, which leaves a bare /metrics free for the Prometheus text
// format when it lands: the two answer the same question for different
// readers, and neither should have to move to make room for the other.
func RegisterMetricsRoutes(r chi.Router, h *Handler) {
	r.Get("/metrics", h.Report)
}

// RegisterPrometheusRoute mounts the exposition format at a bare /metrics,
// where every scraper expects to find it.
//
// Registered at the root rather than under /api, and by the caller rather
// than here, because it is the one route outside the JSON API: it must still
// be wrapped in the same RequireAuth, which is the caller's to apply (JD-033).
func RegisterPrometheusRoute(r chi.Router, h *Handler) {
	r.Get("/metrics", h.Prometheus)
}
