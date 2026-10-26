package images

import (
	"encoding/json"
	"errors"
	"io"
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

func RegisterImageRoutes(r chi.Router, h *Handler) {
	r.Route("/vm-images", func(r chi.Router) {
		r.Get("/", h.List)
		r.Get("/{name}", h.Get)
		r.Put("/{name}", h.Prepare)
		r.Delete("/{name}", h.Remove)
	})
}

// prepareRequest is the body of PUT /api/vm-images/{name}. It may be empty.
type prepareRequest struct {
	// Source is the catalog entry to prepare the image from. Empty means the
	// image's own name, when that is a source.
	Source string `json:"source"`
}

// List returns every VM image and the sources they can be prepared from.
//
// @Summary      List VM images
// @Description  Image templates for VMs (JD-037): a cloud image's disk in a zvol under
// @Description  `.images` in vm-bhyve's default datastore, sealed with an `@base` snapshot
// @Description  that guests' disks are cloned from.
// @Description
// @Description  Nothing is stored (JD-011). `state` is `ready` when `@base` exists and
// @Description  `written` when the zvol does and `@base` does not, which is a write that
// @Description  stopped. `fetching` and `writing` are this process's work in flight, with
// @Description  `running` saying which step (JD-008). `source` and `sha512` are user
// @Description  properties on the zvol: what it was written from, and the checksum of the
// @Description  exact archive. `guests` are the guests cloned from it.
// @Description
// @Description  `sources` is what can be prepared, with whether its verified archive is
// @Description  already on the host. When vm-bhyve is not ready, or its datastore is not on
// @Description  ZFS, the answer is a 200 that says so in `vm_bhyve` and `problem`, with no
// @Description  images.
// @Tags         vm-images
// @Produce      json
// @Success      200  {object}  Listing
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /vm-images [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	listing, err := h.service.List(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, listing)
}

// Get returns one VM image.
//
// @Summary      Show one VM image
// @Description  The same fields as the listing. An image being fetched has no zvol yet and
// @Description  is still here, with its `running` step: the resource a client was told to
// @Description  watch exists from the moment the work starts (JD-008).
// @Tags         vm-images
// @Produce      json
// @Param        name  path      string  true  "Image name"
// @Success      200   {object}  Image
// @Failure      400   {object}  common.ErrorResponse  "Not an image name"
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such image"
// @Security     BearerAuth
// @Router       /vm-images/{name} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	image, err := h.service.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, image)
}

// Prepare starts building a VM image and answers without waiting.
//
// @Summary      Prepare a VM image
// @Description  Three stages, each audited (JD-037):
// @Description
// @Description  1. **fetch-image**: the source's archive and the publisher's checksum file.
// @Description     The archive is downloaded as `.part` and renamed only once its SHA-512
// @Description     matches, so an archive under its own name is a verified one. Skipped when
// @Description     the source's archive is already there.
// @Description  2. **write-image**: a sparse zvol the size of the disk in the archive, with
// @Description     `volmode=dev` so the host never reads the guest's partition table, and the
// @Description     disk written straight into it by `tar -xO`.
// @Description  3. **seal-image**: the `@base` snapshot, which makes it ready.
// @Description
// @Description  **202 and no waiting** (JD-008): minutes of work, and the client watches
// @Description  `GET /vm-images/{name}`. **200** when it was already ready, so a client that
// @Description  timed out can ask again.
// @Description
// @Description  The body may be empty: the source is then the image's own name, so
// @Description  `PUT /vm-images/debian-13` prepares Debian 13.
// @Description
// @Description  **409** when the image exists and is not ready (a write that stopped:
// @Description  remove it and prepare it again; the archive is kept), was written from
// @Description  another source, is already being prepared, or when vm-bhyve or its
// @Description  datastore cannot hold images.
// @Tags         vm-images
// @Accept       json
// @Produce      json
// @Param        name     path      string          true   "The image's name"
// @Param        prepare  body      prepareRequest  false  "The source"
// @Success      200      {object}  Image  "Already there and ready"
// @Success      202      {object}  Image  "Preparing; watch the image"
// @Failure      400      {object}  common.ErrorResponse  "A bad name, or not a source"
// @Failure      401      {object}  common.ErrorResponse
// @Failure      409      {object}  common.ErrorResponse  "Not ready, another source, busy, or no store"
// @Security     BearerAuth
// @Router       /vm-images/{name} [put]
func (h *Handler) Prepare(w http.ResponseWriter, r *http.Request) {
	var body prepareRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		common.HandlerError(w, common.NewAppError(http.StatusBadRequest,
			"could not read the request body: "+err.Error()))
		return
	}
	image, started, err := h.service.Prepare(r.Context(), chi.URLParam(r, "name"), body.Source)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	if started {
		common.WriteJSON(w, http.StatusAccepted, image)
		return
	}
	common.WriteJSON(w, http.StatusOK, image)
}

// Remove destroys a VM image.
//
// @Summary      Remove a VM image
// @Description  Destroys the zvol and its `@base`. The archive stays: it belongs to the
// @Description  source, and preparing the image again starts at the write.
// @Description
// @Description  **409 while any guest's disk is cloned from it**, naming the guests. ZFS
// @Description  would refuse too, in terms of datasets. `confirm` is the image's name.
// @Tags         vm-images
// @Produce      json
// @Param        name     path   string  true  "The image to remove"
// @Param        confirm  query  string  true  "The image's name again"
// @Success      200  {object}  Removal
// @Failure      400  {object}  common.ErrorResponse  "Missing or mismatched confirm"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "No such image"
// @Failure      409  {object}  common.ErrorResponse  "Guests use it, or it is being prepared"
// @Security     BearerAuth
// @Router       /vm-images/{name} [delete]
func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	removal, err := h.service.Delete(r.Context(), chi.URLParam(r, "name"), r.URL.Query().Get("confirm"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, removal)
}
