package audit

import (
	"net/http"
	"strconv"

	"codeberg.org/Otter-Labs/jaildeck/internal/common"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

type AuditHandler struct {
	service *AuditService
}

func NewAuditHandler(service *AuditService) *AuditHandler {
	return &AuditHandler{service: service}
}

func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter, err := parseFilter(q)
	if err != nil {
		common.HandlerError(w, common.NewAppError(http.StatusBadRequest, err.Error()))
		return
	}

	entries, err := h.service.Recent(r.Context(), normalizeLimit(q.Get("limit")), filter)
	if err != nil {
		common.HandlerError(w, err)
		return
	}

	common.WriteJSON(w, http.StatusOK, entries)
}

func normalizeLimit(limitParam string) int {
	limit, err := strconv.Atoi(limitParam)
	if err != nil || limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return limit
}
