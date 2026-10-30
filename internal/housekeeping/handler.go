package housekeeping

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/common"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes mounts the report beside the host's other reports, under
// /host.
func RegisterRoutes(r chi.Router, h *Handler) {
	r.Get("/reclaimable", h.Reclaimable)
}

// Reclaimable lists what storage is tied up in things nothing uses.
//
// @Summary      What storage nothing uses
// @Description  Read now, from ZFS and the file system (#125, JD-049). Removes nothing.
// @Description
// @Description  - **templates** that nothing is cloned from and nothing holds;
// @Description  - **VM images** that no guest's disk is cloned from;
// @Description  - **retained datasets**, every one: kept past their jail on purpose, and
// @Description    listed so that the purpose is remembered;
// @Description  - **snapshots** of a jail's own dataset older than `older_than_days` (30 by
// @Description    default). Snapshots another tool takes and prunes, `zrepl_`, `autosnap_`
// @Description    and `zfs-auto-snap`, are never listed, and `skipped_snapshots` counts them;
// @Description  - **downloaded archives**: a release's `base.txz` and an image's archive.
// @Description    Preparing again downloads them again. No endpoint removes one.
// @Description
// @Description  Each item has its `size`, why it counts, and `remove`: the existing endpoint,
// @Description  confirm included, which keeps its own refusals. A source that cannot be read
// @Description  is named in `problems`, and the rest are still listed.
// @Tags         host
// @Produce      json
// @Param        older_than_days  query  int  false  "Snapshot age, in days, from 1 to 3650"  default(30)
// @Success      200  {object}  Report
// @Failure      400  {object}  common.ErrorResponse  "older_than_days out of range"
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /host/reclaimable [get]
func (h *Handler) Reclaimable(w http.ResponseWriter, r *http.Request) {
	age := DefaultSnapshotAge
	if raw := r.URL.Query().Get("older_than_days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			common.HandlerError(w, common.NewAppError(http.StatusBadRequest,
				"older_than_days is "+strconv.Quote(raw)+"; it has to be a number of days"))
			return
		}
		age = n
	}
	report, err := h.service.Reclaimable(r.Context(), age)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, report)
}
