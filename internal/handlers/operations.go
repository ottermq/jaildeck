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

func NewOperationHandler(service *services.OperationService, renderer *views.Renderer) *OperationHandler {
	return &OperationHandler{service: service, renderer: renderer}
}

func (h *OperationHandler) List(w http.ResponseWriter, r *http.Request) {
	filters := make(map[string]string)
	qOperation := r.URL.Query().Get("operation")
	switch qOperation {
	case "start", "stop", "restart":
		filters["operation"] = qOperation
	default:
	}
	qSuccess := r.URL.Query().Get("success")
	switch qSuccess {
	case "true", "false":
		filters["success"] = qSuccess
	default:
	}
	if qTarget := r.URL.Query().Get("targets"); qTarget != "" {
		filters["targets"] = qTarget
	}
	qLimit := r.URL.Query().Get("limit")
	limit, err := strconv.Atoi(qLimit)
	if err != nil || limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	entries, err := h.service.Recent(r.Context(), limit, filters)
	if err != nil {
		http.Error(w, "failed to list operations", http.StatusInternalServerError)
		return
	}

	data := struct {
		Title   string
		Entries []operations.Entry
	}{
		Title:   "Operations",
		Entries: entries,
	}

	if err := h.renderer.Render(w, "operations", data); err != nil {
		log.Printf("failed to render page: %s", err.Error())
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}
