package firewall

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

// Report answers what PF is doing.
//
// @Summary      PF's state, rules, NAT, anchors and tables
// @Description  What the firewall is doing, read-only (JD-020, JD-034). Nothing reachable from
// @Description  this API can change PF.
// @Description
// @Description  **`state` is `not_loaded`, `disabled` or `enabled`, and none of them is an
// @Description  error.** Not loaded is the common case: the reference hosts do not load PF, and
// @Description  that answer is a `200` with nothing else in it. **Disabled with rules** is a
// @Description  policy that is loaded and not being applied — the rules read exactly as they
// @Description  would if they were, which is why `state` is the field to look at first.
// @Description
// @Description  `for_seconds` is pfctl's own count: the time since PF was last enabled or
// @Description  disabled — **or since boot when neither has happened**. A PF loaded and never
// @Description  enabled reports the host's uptime as its time disabled.
// @Description
// @Description  `rules` and `nat` are separate because `pfctl` keeps them separate: `-s rules`
// @Description  does not show a single NAT rule. An anchor's own rules are not in the main
// @Description  listing either — the main ruleset shows `anchor "jaildeck/*" all` — so every
// @Description  anchor is listed in `anchors` with its rules and NAT. An anchor that could not
// @Description  be read carries an `error` and the rest of the report stands.
// @Description
// @Description  **A rule's `text` is pfctl's own rendering**, normalised by pfctl and not
// @Description  re-parsed. Only what has to be a number is taken out of it: `number` (the `@N`
// @Description  that pflog refers to), the counters, `last_active`, `label`, and the first word
// @Description  as `action`.
// @Description
// @Description  **Tables carry no address count.** `pfctl -s Tables -vv` reports one, and on
// @Description  15.1 it is wrong — `Addresses: 1` for a table holding three. The count is the
// @Description  length of `GET /api/firewall/tables/{name}`.
// @Description
// @Description  `notices` are anything pfctl printed to stderr while succeeding, other than the
// @Description  ALTQ notice it prints on every verbose call.
// @Tags         firewall
// @Produce      json
// @Success      200  {object}  Report
// @Failure      401  {object}  common.ErrorResponse
// @Failure      500  {object}  common.ErrorResponse  "PF is loaded and could not be read"
// @Failure      501  {object}  common.ErrorResponse  "This Jail Deck has no host access wired"
// @Security     BearerAuth
// @Router       /firewall [get]
func (h *Handler) Report(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.Report(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, report)
}

// Table answers one table's addresses.
//
// @Summary      One PF table's addresses
// @Description  `pfctl -t <name> -T show`. The name must be one `GET /api/firewall` lists;
// @Description  any other is a `404` decided before PF is asked. Read when a table is opened
// @Description  rather than with the report, because a blocklist table can hold a hundred
// @Description  thousand entries.
// @Tags         firewall
// @Produce      json
// @Param        name  path  string  true  "Table name, as listed"
// @Success      200  {object}  TableContents
// @Failure      400  {object}  common.ErrorResponse  "Not a PF table name"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "PF has no table by that name"
// @Failure      409  {object}  common.ErrorResponse  "PF is not loaded"
// @Failure      501  {object}  common.ErrorResponse  "This Jail Deck has no host access wired"
// @Security     BearerAuth
// @Router       /firewall/tables/{name} [get]
func (h *Handler) Table(w http.ResponseWriter, r *http.Request) {
	contents, err := h.service.Table(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, contents)
}

// Log answers the most recent pflog lines.
//
// @Summary      The most recent pflog lines
// @Description  `tcpdump -n -e -tttt -r <pflog_logfile>`, the last `lines` of it (100 by default,
// @Description  1000 at most).
// @Description
// @Description  **The file lags the packets.** pflogd buffers and flushes every
// @Description  `flush_delay_seconds` — 60 unless `pflog_flags` sets `-d` — and Jail Deck does
// @Description  not signal it to flush sooner: that would be acting on a daemon it does not own
// @Description  (JD-034). **`log` records the packet that creates a state**, not every packet,
// @Description  so a rule whose counters say thousands may have one line here.
// @Description
// @Description  A line is always in `raw`. Where it has the measured shape it is also taken
// @Description  apart: `rule_number` is the main-ruleset number, which is unambiguous;
// @Description  `anchor` is only the **last component** of an anchor's name, because that is all
// @Description  tcpdump prints — `jaildeck/web` logs as `web`.
// @Description
// @Description  `found: false` means there is no file: nothing has been logged, or pflogd is not
// @Description  running. **`truncated: true` comes with no lines**: the log was too large to
// @Description  read to its end, and the lines that were read are the oldest, not the most
// @Description  recent.
// @Tags         firewall
// @Produce      json
// @Param        lines  query  int  false  "How many lines, 1 to 1000"  default(100)
// @Success      200  {object}  LogReport
// @Failure      400  {object}  common.ErrorResponse  "lines out of range"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      500  {object}  common.ErrorResponse  "The log could not be read"
// @Failure      501  {object}  common.ErrorResponse  "This Jail Deck has no host access wired"
// @Security     BearerAuth
// @Router       /firewall/log [get]
func (h *Handler) Log(w http.ResponseWriter, r *http.Request) {
	lines := 0
	if raw := r.URL.Query().Get("lines"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			common.HandlerError(w, common.NewAppError(http.StatusBadRequest,
				"lines must be a whole number, not "+strconv.Quote(raw)))
			return
		}
		if parsed == 0 {
			// Zero is the service's "use the default"; from a client it is a
			// request for nothing, which is out of range.
			parsed = -1
		}
		lines = parsed
	}
	report, err := h.service.Log(r.Context(), lines)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, report)
}
