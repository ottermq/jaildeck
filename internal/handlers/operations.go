package handlers

import (
	"log"
	"net/http"
	"strconv"

	"github.com/ottermq/jaildeck/internal/operations"
	"github.com/ottermq/jaildeck/internal/services"
	"github.com/ottermq/jaildeck/internal/views"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

type OperationHandler struct {
	service  *services.OperationService
	renderer *views.Renderer
}

type OperationFilterView struct {
	Operation string
	Targets   string
	Success   string
}

func NewOperationHandler(service *services.OperationService, renderer *views.Renderer) *OperationHandler {
	return &OperationHandler{service: service, renderer: renderer}
}

func (h *OperationHandler) List(w http.ResponseWriter, r *http.Request) {
	operationParam := r.URL.Query().Get("operation")
	successParam := r.URL.Query().Get("success")
	targetsParam := r.URL.Query().Get("targets")
	limitParam := r.URL.Query().Get("limit")

	filters := buildOperationFilters(operationParam, successParam, targetsParam)

	limit := normalizeLimit(limitParam)

	entries, err := h.service.Recent(r.Context(), limit, filters)
	if err != nil {
		http.Error(w, "failed to list operations", http.StatusInternalServerError)
		return
	}

	data := struct {
		Title   string
		Entries []operations.Entry
		Filter  OperationFilterView
	}{
		Title:   "Operations",
		Entries: entries,
		Filter: OperationFilterView{
			Operation: operationParam,
			Success:   successParam,
			Targets:   targetsParam,
		},
	}

	if err := h.renderer.Render(w, "operations", data); err != nil {
		log.Printf("failed to render page: %s", err.Error())
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}

	// if err := h.renderer.RenderComponent(w, "operations", "components/operation_form.html", data); err != nil {
	// 	http.Error(w, "failed to render operation form", http.StatusInternalServerError)
	// }
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
