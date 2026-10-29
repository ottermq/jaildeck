package networks

import (
	"encoding/json"
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
	// Tenant networks (JD-043). "default" is the default network's, above,
	// and no tenant network may take the name.
	r.Get("/networks", h.List)
	r.Post("/networks", h.Create)
	r.Get("/networks/{name}", h.Get)
	r.Delete("/networks/{name}", h.Destroy)
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

// List reads every tenant network.
//
// @Summary      Tenant networks
// @Description  Every tenant network on this host (JD-043): a bridge Jail Deck owns, `jdn<i>`,
// @Description  the host's address on it as the gateway, and a vm-bhyve switch on top named
// @Description  after the network. Read off the host: the name and tenant are in the bridge's
// @Description  description, the subnet is its address, the members are on it now. `range` is
// @Description  the block subnets are allocated from (`JAILDECK_NETWORK_RANGE`). The default
// @Description  network is at `/networks/default` and is not listed here.
// @Tags         networks
// @Produce      json
// @Success      200  {object}  NetworkList
// @Failure      401  {object}  common.ErrorResponse
// @Failure      500  {object}  common.ErrorResponse  "The bridges could not be read"
// @Failure      501  {object}  common.ErrorResponse  "This Jail Deck has no host access wired"
// @Security     BearerAuth
// @Router       /networks [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.Networks(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, list)
}

// Get reads one tenant network.
//
// @Summary      A tenant network
// @Description  One tenant network, read off the host as the listing reads it (JD-043). `name`
// @Description  is its vm-bhyve switch -- what a guest's NIC is put on -- and `bridge` the
// @Description  interface a VNET jail's epair joins; the two are different names on purpose,
// @Description  since an interface name holds 15 characters at most. `default` is not a tenant
// @Description  network: the default network is `GET /networks/default`.
// @Tags         networks
// @Produce      json
// @Param        name  path  string  true  "Network name"
// @Success      200  {object}  Network
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "No network by that name"
// @Security     BearerAuth
// @Router       /networks/{name} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	network, err := h.service.Network(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, network)
}

// Create makes a tenant network.
//
// @Summary      Create a tenant network
// @Description  A network of its own for a tenant's guests and VNET jails (#118, JD-043), in
// @Description  four audited steps:
// @Description
// @Description  1. **create-network-bridge**: a bridge `jdn<i>`, described with the network's name
// @Description     and `tenant`, on the lowest /24 of the range that no network has and no
// @Description     address on the host overlaps.
// @Description  2. **set-network-address**: the host's address on it, `.1`, the gateway.
// @Description  3. **create-vm-switch**: a vm-bhyve manual switch on the bridge, named after the
// @Description     network: what a guest's NIC is put on. A VNET jail joins the bridge itself.
// @Description  4. **write-pf-anchor** and, when PF is up, **load-pf-anchor**: the network's NAT,
// @Description     and the rules that let it reach its own gateway and the outside and nothing
// @Description     else of the host's or of another network's, in the same `jaildeck/vmnet`
// @Description     anchor as the default network's, so `pf.conf` needs nothing more.
// @Description
// @Description  A step that fails takes back what the earlier ones made: a network without its
// @Description  rules would be one another tenant can reach. Measured on isengard: with
// @Description  forwarding on and no rules, the host routes one tenant's guests straight into
// @Description  another's.
// @Tags         networks
// @Accept       json
// @Produce      json
// @Param        network  body      CreateNetworkRequest  true  "The network"
// @Success      201      {object}  Network
// @Failure      400      {object}  common.ErrorResponse  "Not a network name, or not a tenant label"
// @Failure      401      {object}  common.ErrorResponse
// @Failure      409      {object}  common.ErrorResponse  "The name is a switch already, or the range is full"
// @Failure      500      {object}  common.ErrorResponse  "A step failed; what it made was taken back"
// @Security     BearerAuth
// @Router       /networks [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateNetworkRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		common.HandlerError(w, common.NewAppError(http.StatusBadRequest,
			"could not read the request body: "+err.Error()))
		return
	}
	network, err := h.service.CreateNetwork(r.Context(), req)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusCreated, network)
}

// Destroy takes an empty tenant network away.
//
// @Summary      Destroy a tenant network
// @Description  Its vm-bhyve switch, its bridge, and its rules (JD-043). **Refused while anything
// @Description  is on it**: a guest whose configuration names its switch, running or not, a jail
// @Description  whose configuration names its bridge, or any interface still a member. The
// @Description  refusal names them. `confirm` is the network's name.
// @Tags         networks
// @Produce      json
// @Param        name     path   string  true  "Network name"
// @Param        confirm  query  string  true  "The network's name again"
// @Success      200  {object}  NetworkRemoval
// @Failure      400  {object}  common.ErrorResponse  "Missing or mismatched confirm"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "No network by that name"
// @Failure      409  {object}  common.ErrorResponse  "Something is on it"
// @Security     BearerAuth
// @Router       /networks/{name} [delete]
func (h *Handler) Destroy(w http.ResponseWriter, r *http.Request) {
	removal, err := h.service.DestroyNetwork(r.Context(), chi.URLParam(r, "name"), r.URL.Query().Get("confirm"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, removal)
}
