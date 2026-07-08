package handlers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/domain"
	"github.com/ottermq/jaildeck/internal/services"
	"github.com/ottermq/jaildeck/internal/views"
)

type JailHandler struct {
	service  *services.JailService
	renderer *views.Renderer
}

type OperationResultView struct {
	Success bool
	Message string
}

type JailActionResultView struct {
	Jail   domain.Jail
	Result OperationResultView
}

func NewJailHandler(jailService *services.JailService, renderer *views.Renderer) *JailHandler {
	return &JailHandler{
		service:  jailService,
		renderer: renderer,
	}
}

func (h *JailHandler) List(w http.ResponseWriter, r *http.Request) {
	jails, err := h.service.List(r.Context())
	if err != nil {
		http.Error(w, "failed to list jails", http.StatusInternalServerError)
		return
	}

	data := struct {
		Title string
		Jails any
	}{
		Title: "Jails",
		Jails: jails,
	}

	if err := h.renderer.Render(w, "jails", data); err != nil {
		fmt.Printf("failed to render page: %s", err.Error())
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

func (h *JailHandler) Start(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var result OperationResultView
	jail, err := h.service.Start(r.Context(), name)
	if err != nil {
		result = OperationResultView{
			Success: false,
			Message: fmt.Sprintf("Failed to start jail %q.", name),
		}
	} else {
		result = OperationResultView{
			Success: true,
			Message: fmt.Sprintf("Started jail %q.", name),
		}
	}

	data := JailActionResultView{
		Jail:   jail,
		Result: result,
	}

	if err := h.renderer.RenderComponent(w, "jails", "components/jail_action_result.html", data); err != nil {
		http.Error(w, "failed to render jail action result", http.StatusInternalServerError)
	}
}

func (h *JailHandler) Stop(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var result OperationResultView
	jail, err := h.service.Stop(r.Context(), name)
	if err != nil {
		result = OperationResultView{
			Success: false,
			Message: fmt.Sprintf("Failed to stop jail %q.", name),
		}
	} else {
		result = OperationResultView{
			Success: true,
			Message: fmt.Sprintf("Stopped jail %q.", name),
		}
	}

	data := JailActionResultView{
		Jail:   jail,
		Result: result,
	}

	if err := h.renderer.RenderComponent(w, "jails", "components/jail_action_result.html", data); err != nil {
		http.Error(w, "failed to render jail action result", http.StatusInternalServerError)
	}
}

func (h *JailHandler) Restart(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var result OperationResultView
	jail, err := h.service.Restart(r.Context(), name)
	if err != nil {
		result = OperationResultView{
			Success: false,
			Message: fmt.Sprintf("Failed to restart jail %q.", name),
		}
	} else {
		result = OperationResultView{
			Success: true,
			Message: fmt.Sprintf("Restarted jail %q.", name),
		}
	}

	data := JailActionResultView{
		Jail:   jail,
		Result: result,
	}

	if err := h.renderer.RenderComponent(w, "jails", "components/jail_action_result.html", data); err != nil {
		http.Error(w, "failed to render jail action result", http.StatusInternalServerError)
	}
}
