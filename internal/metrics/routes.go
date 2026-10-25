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
