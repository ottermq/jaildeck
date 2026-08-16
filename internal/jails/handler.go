package jails

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/views"
)

type JailHandler struct {
	service  *JailService
	renderer *views.Renderer
}

type OperationResultView struct {
	Success bool
	Message string
}

type JailActionResultView struct {
	Jail   Jail
	Result OperationResultView
}

func NewJailHandler(jailService *JailService, renderer *views.Renderer) *JailHandler {
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
		log.Printf("failed to render page: %s", err.Error())
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

func (h *JailHandler) do(w http.ResponseWriter, r *http.Request, action, verbPast string, call func(context.Context, JailName) (Jail, error)) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}

	var result OperationResultView
	jail, err := call(r.Context(), name)
	if err != nil {
		result = OperationResultView{
			Success: false,
			Message: operationFailureMessage(action, name.String(), err),
		}
	} else {
		result = OperationResultView{
			Success: true,
			Message: fmt.Sprintf("%s jail %q.", verbPast, name),
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

func (h *JailHandler) Start(w http.ResponseWriter, r *http.Request) {
	h.do(w, r, "start", "Started", h.service.Start)
}

func (h *JailHandler) Stop(w http.ResponseWriter, r *http.Request) {
	h.do(w, r, "stop", "Stopped", h.service.Stop)
}

func (h *JailHandler) Restart(w http.ResponseWriter, r *http.Request) {
	h.do(w, r, "restart", "Restarted", h.service.Restart)
}

func operationFailureMessage(action, name string, err error) string {
	if err == nil {
		return ""
	}
	var errMsg string
	var cmdErr *system.CommandError
	if errors.As(err, &cmdErr) {
		errMsg = cmdErr.Summary()
	} else {
		errMsg = err.Error()
	}

	return fmt.Sprintf("Failed to %s jail %q: %s", action, name, errMsg)
}
