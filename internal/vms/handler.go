package vms

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/common"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// List answers every guest vm-bhyve knows.
//
// @Summary      vm-bhyve's guests and their state
// @Description  `vm list`, read-only (JD-019, JD-035). Nothing reachable from this API can start,
// @Description  stop or change a guest yet.
// @Description
// @Description  **`vm_bhyve` is `ready`, `not_installed` or `not_configured`, and none of them is
// @Description  an error.** Not configured carries vm-bhyve's own words in `problem` — the same
// @Description  ones Host Setup reports, with the remedy beside them there.
// @Description
// @Description  `state` is vm-bhyve's: `running` and `bootloader` carry the `pid` of the bhyve
// @Description  or loader process; `stopped`; `suspended`, a guest stopped with its state saved;
// @Description  `locked`, marked running by the host in `locked_by`, in a datastore shared
// @Description  between hosts. **Read on every request and never stored** (JD-011).
// @Description
// @Description  `cpu` and `memory` are the guest's configuration as written (`"2"`, `"512M"`),
// @Description  not re-parsed. `autostart` is whether the guest is in `rc.conf`'s `vm_list`, and
// @Description  `autostart_order` its place there.
// @Tags         vms
// @Produce      json
// @Success      200  {object}  Listing
// @Failure      401  {object}  common.ErrorResponse
// @Failure      500  {object}  common.ErrorResponse  "vm-bhyve answered and could not be read"
// @Failure      501  {object}  common.ErrorResponse  "This Jail Deck has no host access wired"
// @Security     BearerAuth
// @Router       /vms [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	listing, err := h.service.List(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, listing)
}

// Get answers one guest.
//
// @Summary      One guest: its row, consoles, network interfaces and disks
// @Description  The guest's row from `GET /api/vms`, and what `vm info <name>` adds. The name
// @Description  must be one the listing has; any other is a `404`, and one vm-bhyve could never
// @Description  have created is a `400`.
// @Description
// @Description  `memory_resident` and `consoles` exist only while the guest's bhyve instance
// @Description  does. A console is the host's end of a null-modem pair (`/dev/nmdm-<name>.1B`).
// @Description  A network's `device` is the tap of the current run — a new one on every start —
// @Description  and `size` and `used` are bytes.
// @Tags         vms
// @Produce      json
// @Param        name  path  string  true  "Guest name, as listed"
// @Success      200  {object}  Detail
// @Failure      400  {object}  common.ErrorResponse  "Not a vm-bhyve guest name"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "vm-bhyve has no guest by that name"
// @Failure      409  {object}  common.ErrorResponse  "vm-bhyve is not installed or not configured"
// @Failure      500  {object}  common.ErrorResponse  "vm-bhyve answered and could not be read"
// @Failure      501  {object}  common.ErrorResponse  "This Jail Deck has no host access wired"
// @Security     BearerAuth
// @Router       /vms/{name} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	detail, err := h.service.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, detail)
}
