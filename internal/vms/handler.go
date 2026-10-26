package vms

import (
	"context"
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
// @Description  `vm list`, read on every request (JD-019, JD-035).
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

// Start starts a guest.
//
// @Summary      Start a guest
// @Description  `vm start <name>`, detached from Jail Deck so that nothing that happens to the
// @Description  command can reach the guest, and then confirmed: vm-bhyve has to list it as
// @Description  `running` within 15 seconds (JD-036). A guest already running, or in its boot
// @Description  loader, is answered as it is and nothing runs. Audited as `vm-start`.
// @Description
// @Description  A client disconnecting does not abort it.
// @Tags         vms
// @Produce      json
// @Param        name  path  string  true  "Guest name"
// @Success      200  {object}  OperationResult
// @Failure      400  {object}  common.ErrorResponse  "Not a vm-bhyve guest name"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "vm-bhyve has no guest by that name"
// @Failure      409  {object}  common.ErrorResponse  "Locked by another host, or vm-bhyve not ready"
// @Failure      500  {object}  common.ErrorResponse  "vm-bhyve refused, or the guest did not come up"
// @Failure      504  {object}  common.ErrorResponse  "Timed out and may not have completed"
// @Security     BearerAuth
// @Router       /vms/{name}/start [post]
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	h.operate(w, r, h.service.Start)
}

// Stop stops a guest.
//
// @Summary      Stop a guest, powering it off if it does not answer
// @Description  `vm stop <name>` — the ACPI power button — and then up to 60 seconds for vm-bhyve
// @Description  to list the guest as `stopped`. **A guest that has not stopped by then is powered
// @Description  off** with `vm poweroff -f`, the equivalent of pulling the plug, and the answer
// @Description  says `forced: true` (JD-019, JD-036). A guest whose operating system is still
// @Description  booting ignores the power button, so stopping one straight after starting it ends
// @Description  that way.
// @Description
// @Description  A stopped or suspended guest is answered as it is. One in its boot loader is a
// @Description  `409`: vm-bhyve asks on a terminal before killing a loader, and Jail Deck has none.
// @Description  Audited as `vm-stop`, and `vm-poweroff` when it came to that.
// @Tags         vms
// @Produce      json
// @Param        name  path  string  true  "Guest name"
// @Success      200  {object}  OperationResult
// @Failure      400  {object}  common.ErrorResponse  "Not a vm-bhyve guest name"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "vm-bhyve has no guest by that name"
// @Failure      409  {object}  common.ErrorResponse  "Locked, in its boot loader, or vm-bhyve not ready"
// @Failure      500  {object}  common.ErrorResponse  "vm-bhyve refused, or the guest did not stop"
// @Failure      504  {object}  common.ErrorResponse  "Timed out and may not have completed"
// @Security     BearerAuth
// @Router       /vms/{name}/stop [post]
func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	h.operate(w, r, h.service.Stop)
}

// Restart stops a running guest and starts it again.
//
// @Summary      Restart a guest
// @Description  Stop, as `POST /vms/{name}/stop` does it — powering off a guest that does not
// @Description  answer within 60 seconds, and saying `forced: true` — then start. A stopped guest
// @Description  is only started. Not vm-bhyve's own restart, which asks the guest to reboot and
// @Description  has no deadline (JD-036).
// @Tags         vms
// @Produce      json
// @Param        name  path  string  true  "Guest name"
// @Success      200  {object}  OperationResult
// @Failure      400  {object}  common.ErrorResponse  "Not a vm-bhyve guest name"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "vm-bhyve has no guest by that name"
// @Failure      409  {object}  common.ErrorResponse  "Locked, in its boot loader, or vm-bhyve not ready"
// @Failure      500  {object}  common.ErrorResponse  "vm-bhyve refused, or the guest did not stop or start"
// @Failure      504  {object}  common.ErrorResponse  "Timed out and may not have completed"
// @Security     BearerAuth
// @Router       /vms/{name}/restart [post]
func (h *Handler) Restart(w http.ResponseWriter, r *http.Request) {
	h.operate(w, r, h.service.Restart)
}

func (h *Handler) operate(w http.ResponseWriter, r *http.Request,
	call func(context.Context, string) (OperationResult, error)) {
	result, err := call(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, result)
}
