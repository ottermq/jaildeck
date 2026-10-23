package jails

import (
	"context"
	"encoding/json"
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

func (h *JailHandler) Get(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	jail, err := h.service.Get(r.Context(), name)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, jail)
}

func (h *JailHandler) do(w http.ResponseWriter, r *http.Request, call func(context.Context, JailName) (OperationResult, error)) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}

	result, err := call(r.Context(), name)

	if err != nil {
		common.HandlerError(w, err)
		return
	}

	common.WriteJSON(w, http.StatusOK, result.Jail)

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

// ensureDatasetRequest is the body of PUT /api/jails/{name}/datasets/{child}.
// An object rather than the bare map so the request has somewhere to grow --
// a quota today, a source snapshot when #6 arrives.
type ensureDatasetRequest struct {
	Properties map[string]string `json:"properties"`
}

func (h *JailHandler) EnsureDataset(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var body ensureDatasetRequest
	if err := decodeJSON(r, &body); err != nil {
		common.HandlerError(w, err)
		return
	}

	change, err := h.service.EnsureDataset(r.Context(), name, chi.URLParam(r, "child"), body.Properties)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	switch {
	case change.Conflict != nil:
		common.WriteJSON(w, http.StatusConflict, change)
	case change.Created:
		common.WriteJSON(w, http.StatusCreated, change)
	default:
		common.WriteJSON(w, http.StatusOK, change)
	}
}

// SetProperties tags the jail's own dataset. The body is the property map
// itself: a null value removes that property rather than setting it empty,
// which is the difference between "this jail has no instance" and "this jail's
// instance is the empty string".
func (h *JailHandler) SetProperties(w http.ResponseWriter, r *http.Request) {
	h.setProperties(w, r, "")
}

// SetDatasetProperties does the same for a dataset beneath the jail's own.
func (h *JailHandler) SetDatasetProperties(w http.ResponseWriter, r *http.Request) {
	h.setProperties(w, r, chi.URLParam(r, "child"))
}

func (h *JailHandler) setProperties(w http.ResponseWriter, r *http.Request, child string) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var properties map[string]*string
	if err := decodeJSON(r, &properties); err != nil {
		common.HandlerError(w, err)
		return
	}

	found, err := h.service.SetUserProperties(r.Context(), name, child, properties)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, found)
}

// decodeJSON reads a request body, refusing a field the target does not have.
// A client that misspells "properties" should hear about it rather than have
// its request silently do nothing.
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return common.NewAppError(http.StatusBadRequest, "could not read the request body: "+err.Error())
	}
	return nil
}
