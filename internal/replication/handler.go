package replication

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/common"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// Get answers the last read of zrepl's replication cursors.
//
// @Summary      ZFS replication: when each dataset last reached the other side
// @Description  For each zrepl job, each dataset it replicates and when the newest snapshot
// @Description  that reached the other side was taken (#247, JD-062). Read from zrepl's
// @Description  replication cursors, the bookmarks it keeps on the sending side, so a job that
// @Description  another host pulls from is covered too. **Read every minute, never by this
// @Description  request.** `none` is a host with no cursors: zrepl is not replicating from it,
// @Description  or has not finished a first replication. `unknown` is a read that failed, never
// @Description  `none`. Jail Deck sets no threshold: how old is too old depends on the job's
// @Description  interval, which lives in zrepl's configuration.
// @Tags         replication
// @Produce      json
// @Success      200  {object}  Snapshot
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /replication [get]
func (h *Handler) Get(w http.ResponseWriter, _ *http.Request) {
	common.WriteJSON(w, http.StatusOK, h.service.Snapshot())
}

func RegisterRoutes(r chi.Router, h *Handler) {
	r.Get("/replication", h.Get)
}
