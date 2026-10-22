package host

import (
	"errors"
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

func (h *HostHandler) Layout(w http.ResponseWriter, r *http.Request) {
	layout, err := h.service.Layout(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, layout)
}

// EnsureLayout takes no body: the layout is a singleton whose desired state
// is fixed, so the request has nothing to describe. 201 when it created
// something, 200 when everything was already in place (JD-016).
func (h *HostHandler) EnsureLayout(w http.ResponseWriter, r *http.Request) {
	change, err := h.service.EnsureLayout(r.Context())
	var conflict *LayoutConflictError
	if errors.As(err, &conflict) {
		// The usual error shape, plus each dataset that differs, so a client
		// can tell which one without parsing the message.
		common.WriteJSON(w, http.StatusConflict, map[string]any{
			"error":     conflict.Error(),
			"conflicts": conflict.Conflicts,
		})
		return
	}
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	status := http.StatusOK
	if change.Changed {
		status = http.StatusCreated
	}
	common.WriteJSON(w, status, change)
}
