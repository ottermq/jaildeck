package storage

import (
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
func (h *StorageHandler) Retained(w http.ResponseWriter, r *http.Request) {
	datasets, err := h.service.RetainedDatasets(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	retained := make([]retainedDataset, 0, len(datasets))
	for _, dataset := range datasets {
		retained = append(retained, retainedDataset{
			Name:       dataset.Name,
			Short:      lastComponent(dataset.Name),
			Mountpoint: dataset.Mountpoint,
			Used:       dataset.Used,
			Referenced: dataset.Referenced,
		})
	}
	common.WriteJSON(w, http.StatusOK, retained)
}

// retainedDataset carries Short because that is what a restore asks for: the
// name under <pool>/jails/retained, not the full dataset path.
type retainedDataset struct {
	Name       string `json:"name"`
	Short      string `json:"short"`
	Mountpoint string `json:"mountpoint"`
	Used       int64  `json:"used"`
	Referenced int64  `json:"referenced"`
}

func RegisterStorageRoutes(r chi.Router, h *StorageHandler) {
	r.Get("/retained", h.Retained)
}

func lastComponent(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[i+1:]
		}
	}
	return name
}
