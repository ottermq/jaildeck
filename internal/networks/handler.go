package networks

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

func RegisterNetworkRoutes(r chi.Router, h *Handler) {
	r.Get("/networks/default", h.Default)
	r.Put("/networks/default", h.EnsureDefault)
}

// Default reports the default VM network, part by part.
//
// @Summary      The default VM network
// @Description  The network a node's guests get unless asked otherwise (#118, JD-040): a
// @Description  vm-bhyve switch with **private** ports, so guests reach the host and the
// @Description  outside and not each other, the host's address on it as their gateway, and
// @Description  outbound NAT through a PF anchor whose rules Jail Deck writes.
// @Description
// @Description  Every part is read off the host on every request (JD-011), with its
// @Description  `owner`. Jail Deck's parts are put in place by `PUT /api/networks/default`:
// @Description  the switch, its address and private ports, IP forwarding now and at boot,
// @Description  the anchor file, and loading it. The operator's are PF itself and the three
// @Description  lines `pf.conf` includes once; a failing one carries the exact `remedy`.
// @Description
// @Description  `usable` is a guest reaching the host; `ready` is every part, NAT included.
// @Tags         networks
// @Produce      json
// @Success      200  {object}  Default
// @Failure      401  {object}  common.ErrorResponse
// @Failure      409  {object}  common.ErrorResponse  "vm-bhyve is not ready"
// @Security     BearerAuth
// @Router       /networks/default [get]
func (h *Handler) Default(w http.ResponseWriter, r *http.Request) {
	d, err := h.service.Default(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, d)
}

// EnsureDefault puts the default VM network's Jail Deck-owned parts in place.
//
// @Summary      Set up the default VM network
// @Description  Idempotent: only what is missing is done, each step audited, in order:
// @Description  `create-vm-switch`, `set-vm-switch-address`, `set-vm-switch-private`,
// @Description  `enable-ip-forwarding` (`sysctl`), `enable-ip-forwarding-at-boot`
// @Description  (`sysrc gateway_enable=YES`), `write-pf-anchor`, and `load-pf-anchor` when PF is
// @Description  up (validated with `pfctl -n` first; a file it refuses changes nothing).
// @Description  `done` names what this request did; the answer is the network afterwards.
// @Description
// @Description  **It refuses rather than clobbers:** a switch of that name that is not a
// @Description  standard one, or whose address is not `JAILDECK_VM_ADDRESS`, is a `409`
// @Description  before anything runs. It never enables PF or edits `pf.conf` (JD-020):
// @Description  those parts stay failing until the operator does them, with their remedy.
// @Description
// @Description  The same as `jaildeck init vm-network`.
// @Tags         networks
// @Produce      json
// @Success      200  {object}  Ensured
// @Failure      401  {object}  common.ErrorResponse
// @Failure      409  {object}  common.ErrorResponse  "A switch of that name is not Jail Deck's to change, or vm-bhyve is not ready"
// @Failure      500  {object}  common.ErrorResponse  "A step failed; done names those before it"
// @Security     BearerAuth
// @Router       /networks/default [put]
func (h *Handler) EnsureDefault(w http.ResponseWriter, r *http.Request) {
	ensured, err := h.service.EnsureDefault(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, ensured)
}
