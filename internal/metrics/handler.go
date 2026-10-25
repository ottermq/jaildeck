package metrics

import (
	"net/http"

	"github.com/ottermq/jaildeck/internal/common"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Report answers the host's and every running jail's resource usage.
//
// @Summary      Host and per-jail resource usage
// @Description  Load average, CPU, memory and pools for the host, and CPU, memory and a
// @Description  process count for every jail that is **running** — a stopped jail has no
// @Description  usage to read (JD-031).
// @Description
// @Description  One request, because the dashboard is one page. It costs one `rctl -u` per
// @Description  running jail: there is no form that answers for every subject at once.
// @Description  That is also why usage is not part of `GET /api/jails` — a listing is what
// @Description  an operator opens to start a jail, and it should not pay for the page that
// @Description  watches them.
// @Description
// @Description  **`cpu.percent` is absent on the first call after a restart.** `kern.cp_time`
// @Description  is cumulative and there is no kernel-maintained utilisation figure, so a
// @Description  percentage exists only between two readings. The raw `cpu.ticks` are always
// @Description  there, and `cpu.over_seconds` says how long the interval behind the
// @Description  percentage actually was — so a reading five seconds after the last one and
// @Description  one five minutes after are told apart rather than read as the same word.
// @Description
// @Description  A jail's `source` is `rctl` where `kern.racct.enable` is on and `ps` where
// @Description  it is not. Measured on three jails: the `ps` resident sums land within 1% of
// @Description  rctl's `memoryuse` and the process counts are identical, because racct's
// @Description  `memoryuse` **is** the sum of the same per-process resident sizes. `rctl`
// @Description  additionally answers `vmemoryuse`, `openfiles` and `cputime`; `ps` does not,
// @Description  which is what `source` is for.
// @Description
// @Description  `pcpu` is percent of **one core**, so a jail using two whole cores reads 200.
// @Description
// @Description  Memory is a breakdown rather than one "used" figure: on a ZFS host most of
// @Description  `wired` is the ARC, which is reclaimable, and a single number would read as
// @Description  pressure that is not there.
// @Description
// @Description  **Nothing is stored.** There is no history and no sampler; a chart over time
// @Description  is Prometheus's job (JD-011).
// @Tags         metrics
// @Produce      json
// @Success      200  {object}  Report
// @Failure      401  {object}  common.ErrorResponse
// @Failure      500  {object}  common.ErrorResponse  "The host could not be read"
// @Failure      501  {object}  common.ErrorResponse  "This Jail Deck has no host access wired"
// @Security     BearerAuth
// @Router       /metrics [get]
func (h *Handler) Report(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.Report(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, report)
}
