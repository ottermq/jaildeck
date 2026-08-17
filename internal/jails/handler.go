package jails

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/common"
)

type JailHandler struct {
	service *JailService
}

func NewJailHandler(jailService *JailService) *JailHandler {
	return &JailHandler{
		service: jailService,
	}
}

func (h *JailHandler) List(w http.ResponseWriter, r *http.Request) {
	jails, err := h.service.List(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, jails)
}

func (h *JailHandler) do(w http.ResponseWriter, r *http.Request, call func(context.Context, JailName) (Jail, error)) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}

	jail, err := call(r.Context(), name)

	if err != nil {
		common.HandlerError(w, err)
		return
	}

	common.WriteJSON(w, http.StatusOK, jail)

}

func (h *JailHandler) Start(w http.ResponseWriter, r *http.Request) {
	h.do(w, r, h.service.Start)
}

func (h *JailHandler) Stop(w http.ResponseWriter, r *http.Request) {
	h.do(w, r, h.service.Stop)
}

func (h *JailHandler) Restart(w http.ResponseWriter, r *http.Request) {
	h.do(w, r, h.service.Restart)
}
