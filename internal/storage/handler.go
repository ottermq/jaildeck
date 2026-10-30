package storage

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/common"
)

// StorageHandler serves the storage domain's own endpoints -- the ones that
// are not about a particular jail. A domain's handler lives in its own package
// alongside its entity, port and service (JD-010).
type StorageHandler struct {
	service *Service
}

func NewStorageHandler(service *Service) *StorageHandler {
	return &StorageHandler{service: service}
}

// Retained lists the datasets being kept past their jails. Without it a client
// that has retained something has no way to find it again except by having
// remembered the name, and the whole point of retaining is that the jail it
// belonged to is gone.
// Retained lists the datasets being kept past their jails.
//
// @Summary      Retained datasets
// @Description  Everything under <pool>/jails/retained, with the short name a restore
// @Description  asks for. Without this a client that retained something has no way to find
// @Description  it again, and the whole point of retaining is that the jail it belonged to
// @Description  is gone (JD-025).
// @Description
// @Description  Empty on a host that has never retained anything, rather than an error:
// @Description  the retained dataset is created on demand. Each names its snapshots, which a
// @Description  destroy would take with it.
// @Tags         storage
// @Produce      json
// @Success      200  {array}   RetainedDataset
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /retained [get]
func (h *StorageHandler) Retained(w http.ResponseWriter, r *http.Request) {
	datasets, err := h.service.RetainedDatasets(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	snapshots, err := h.service.RetainedSnapshots(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	retained := make([]RetainedDataset, 0, len(datasets))
	for _, dataset := range datasets {
		retained = append(retained, RetainedDataset{
			Name:       dataset.Name,
			Short:      lastComponent(dataset.Name),
			Mountpoint: dataset.Mountpoint,
			Used:       dataset.Used,
			Referenced: dataset.Referenced,
			Snapshots:  append(make([]string, 0), snapshots[dataset.Name]...),
		})
	}
	common.WriteJSON(w, http.StatusOK, retained)
}

// RetainedDataset carries Short because that is what a restore asks for: the
// name under <pool>/jails/retained, not the full dataset path. Exported so the
// generated API spec can name it.
type RetainedDataset struct {
	Name       string `json:"name"`
	Short      string `json:"short"`
	Mountpoint string `json:"mountpoint"`
	Used       int64  `json:"used"`
	Referenced int64  `json:"referenced"`
	// Snapshots are what destroying it would take with it (#120), oldest
	// first, so a confirmation can name them.
	Snapshots []string `json:"snapshots"`
}

// DestroyRetained destroys a retained dataset and its snapshots.
//
// @Summary      Destroy a retained dataset
// @Description  The end of a retain (#120): <pool>/jails/retained/<name> and its snapshots,
// @Description  with zfs destroy -r, never -R. name is the short name from GET /retained, and
// @Description  confirm repeats it, as a jail's destroy repeats the jail's name.
// @Description
// @Description  Refused with 409, before anything runs, while a snapshot of it has a hold or a
// @Description  dependent clone, and when it has datasets of its own: zfs destroy -r would take
// @Description  those silently, and they are not what was named. The answer names each, and the
// @Description  snapshots that would go.
// @Description
// @Description  404 when there is nothing retained under the name, so a client repeating the
// @Description  call after a timeout can tell it is gone.
// @Tags         storage
// @Produce      json
// @Param        name     path      string  true  "The short name from GET /retained"
// @Param        confirm  query     string  true  "The same name again"
// @Success      200      {object}  DatasetDestroy
// @Failure      400      {object}  common.ErrorResponse  "No confirm, or one that does not match"
// @Failure      401      {object}  common.ErrorResponse
// @Failure      404      {object}  common.ErrorResponse  "Nothing retained under that name"
// @Failure      409      {object}  DatasetDestroy        "A hold, a clone, or a dataset of its own is in the way"
// @Security     BearerAuth
// @Router       /retained/{name} [delete]
func (h *StorageHandler) DestroyRetained(w http.ResponseWriter, r *http.Request) {
	// Detached like every other write: a client hanging up must not decide
	// whether the destroy ran (JD-014).
	result, err := h.service.DestroyRetained(context.WithoutCancel(r.Context()),
		chi.URLParam(r, "name"), r.URL.Query().Get("confirm"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	if result.Blocked != nil {
		common.WriteJSON(w, http.StatusConflict, result)
		return
	}
	common.WriteJSON(w, http.StatusOK, result)
}

func RegisterStorageRoutes(r chi.Router, h *StorageHandler) {
	r.Get("/retained", h.Retained)
	r.Delete("/retained/{name}", h.DestroyRetained)
}

func lastComponent(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[i+1:]
		}
	}
	return name
}
