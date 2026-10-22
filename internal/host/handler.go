package host

import (
	"net/http"

	"github.com/ottermq/jaildeck/internal/common"
)

type HostHandler struct {
	service *Service
}

func NewHostHandler(service *Service) *HostHandler {
	return &HostHandler{service: service}
}

func (h *HostHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.Readiness(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, report)
}
