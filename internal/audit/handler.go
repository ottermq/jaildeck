package audit

import (
	"net/http"
	"strconv"

	"github.com/ottermq/jaildeck/internal/common"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

type AuditHandler struct {
	service *AuditService
}

type OperationFilterView struct {
	Operation string
	Targets   string
	Success   string
}

func NewAuditHandler(service *AuditService) *AuditHandler {
	return &AuditHandler{service: service}
}

func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	operationParam := r.URL.Query().Get("operation")
	successParam := r.URL.Query().Get("success")
	targetsParam := r.URL.Query().Get("targets")
	limitParam := r.URL.Query().Get("limit")

	filters := buildOperationFilters(operationParam, successParam, targetsParam)

	limit := normalizeLimit(limitParam)

	entries, err := h.service.Recent(r.Context(), limit, filters)
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

func buildOperationFilters(operationParam string, successParam string, targetsParam string) map[string]string {
	filters := make(map[string]string)
	switch operationParam {
	case "start", "stop", "restart":
		filters["operation"] = operationParam
	default:
	}
	switch successParam {
	case "true", "false":
		filters["success"] = successParam
	default:
	}
	if targetsParam != "" {
		filters["targets"] = targetsParam
	}
	return filters
}
