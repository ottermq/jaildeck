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

// Readiness judges every host check.
//
// @Summary      Host readiness report
// @Description  Every check this host must pass before it can run jails, each pass, fail
// @Description  or unknown with what was observed and the remedy. Nothing here writes: a
// @Description  check that cannot observe reports unknown rather than failing the request.
// @Tags         host
// @Produce      json
// @Success      200  {object}  Report
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /host/readiness [get]
func (h *HostHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.Readiness(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, report)
}

// Layout reports the jails dataset layout.
//
// @Summary      Jails dataset layout
// @Description  Whether <pool>/jails and its media, templates and containers children
// @Description  exist and where they are mounted (JD-006, JD-021). Read-only.
// @Tags         host
// @Produce      json
// @Success      200  {object}  Layout
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /host/layout [get]
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
// EnsureLayout creates whatever the layout is missing.
//
// @Summary      Create the missing layout datasets
// @Description  Idempotent: creates only what is absent and reports what it made. It
// @Description  refuses when a dataset on another pool is already mounted where a missing
// @Description  one would go, because creating it would mount over that pool and over any
// @Description  jails inside it (JD-021).
// @Tags         host
// @Produce      json
// @Success      200  {object}  LayoutChange
// @Failure      401  {object}  common.ErrorResponse
// @Failure      409  {object}  common.ErrorResponse  "Something else is mounted there"
// @Security     BearerAuth
// @Router       /host/layout [put]
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
