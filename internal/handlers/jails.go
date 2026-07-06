package handlers

import (
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/otterlabs/jaildeck/internal/services"
	"github.com/otterlabs/jaildeck/internal/views"
)

type JailHandler struct {
	service  *services.JailService
	renderer *views.Renderer
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

	jail, err := h.service.Start(r.Context(), name)
	if err != nil {
		log.Printf("failed to start jail %q: %v", name, err)
		http.Error(w, "failed to start jail", http.StatusInternalServerError)
		return
	}

	if err := h.renderer.RenderComponent(w, "jails", "components/jail_row.html", jail); err != nil {
		http.Error(w, "failed to render jail row", http.StatusInternalServerError)
	}
}

func (h *JailHandler) Stop(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	jail, err := h.service.Stop(r.Context(), name)
	if err != nil {
		log.Printf("failed to stop jail %q: %v", name, err)
		http.Error(w, "failed to stop jail", http.StatusInternalServerError)
		return
	}

	if err := h.renderer.RenderComponent(w, "jails", "components/jail_row.html", jail); err != nil {
		http.Error(w, "failed to render jail row", http.StatusInternalServerError)
	}
}

func (h *JailHandler) Restart(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	jail, err := h.service.Restart(r.Context(), name)
	if err != nil {
		log.Printf("failed to restart jail %q: %v", name, err)
		http.Error(w, "failed to restart jail", http.StatusInternalServerError)
		return
	}

	if err := h.renderer.RenderComponent(w, "jails", "components/jail_row.html", jail); err != nil {
		http.Error(w, "failed to render jail row", http.StatusInternalServerError)
	}
}
