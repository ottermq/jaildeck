package metrics

import (
	"bytes"
	"net/http"
	"strconv"

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
// @Description  `disks` is every physical device that answers, pooled or not — a spare or a
// @Description  scratch disk can fail too. It is **empty rather than an error** on a host
// @Description  without `smartmontools`, which is a package and not part of the base system,
// @Description  and a disk that cannot be read never fails the rest of the report (JD-032).
// @Description
// @Description  **`passed` is the drive's own overall assessment, not a summary of
// @Description  `failing`.** The two routinely disagree: a drive can report `PASSED` while an
// @Description  attribute has been below threshold for days. `failing` is what smartctl says
// @Description  is below threshold now and `failed` what it says ever was — its judgment,
// @Description  carried as given rather than re-derived from `value` against `threshold`.
// @Description  An attribute's `raw` is the **string** smartctl printed, never a number to
// @Description  reason about: vendors pack several fields into that integer.
// @Description
// @Description  `device` is the name the kernel and ZFS use. Where smartctl needs a different
// @Description  path for the same drive — an NVMe is `nda0` everywhere but `/dev/nvme0` to
// @Description  smartctl, which refuses `/dev/nda0` — `smart_path` says so. An NVMe carries
// @Description  an `nvme` block instead of an attribute table, because it has none.
// @Description
// @Description  Self-test logs are **read, never started**: a long test costs hours of real
// @Description  I/O and choosing when to pay that is an operator's decision.
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

// Prometheus answers the same report in the text exposition format.
//
// Behind the same authentication as everything else: on FreeBSD a jail with
// ip4=inherit shares the host's loopback, so "it only listens on 127.0.0.1"
// is not a boundary and an unauthenticated endpoint would publish every
// jail's usage and every disk's serial to any jail on the box (JD-033).
//
// Not in the OpenAPI spec, deliberately. The spec describes the JSON API and
// this is not JSON; the exposition format documents itself in its own HELP
// lines, which is what a scraper's user reads.
func (h *Handler) Prometheus(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.Report(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}

	// Rendered whole before anything is sent. A write that fails halfway
	// through would already have sent 200 and a truncated body, which a
	// scraper reads as a successful scrape of a host that lost half its
	// disks.
	var body bytes.Buffer
	if err := WriteExposition(&body, report); err != nil {
		common.HandlerError(w, common.NewAppError(http.StatusInternalServerError,
			"could not render the metrics: "+err.Error()))
		return
	}

	w.Header().Set("Content-Type", ExpositionContentType)
	w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}
