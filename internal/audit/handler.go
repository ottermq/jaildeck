package audit

import (
	"net/http"
	"strconv"

	"github.com/ottermq/jaildeck/internal/common"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

type AuditHandler struct {
	service *AuditService
}

func NewAuditHandler(service *AuditService) *AuditHandler {
	return &AuditHandler{service: service}
}

// List returns recent operations, newest first.
//
// @Summary      Operation history
// @Description  What Jail Deck did: timestamp, operation, target, the command it ran, exit
// @Description  code and whether it timed out (JD-011). Filters combine, and all of them
// @Description  are applied to the whole file rather than to the page.
// @Description
// @Description  The log lives in /var/log and is rotated by newsyslog, so after a rotation
// @Description  this sees only the current file. History appears to truncate with nothing
// @Description  in the response to say so: that is the accepted cost of treating a log as
// @Description  a log.
// @Tags         audit
// @Produce      json
// @Param        limit       query     int     false  "How many entries, up to 500"  default(50)
// @Param        operation   query     string  false  "Match one operation, case-insensitively"
// @Param        targets     query     string  false  "Comma-separated targets"
// @Param        success     query     bool    false  "Only successes, or only failures"
// @Param        timed_out   query     bool    false  "Only operations that timed out"
// @Param        request_id  query     string  false  "Exact, case-sensitive X-Request-Id"
// @Param        before      query     string  false  "Cursor from a previous page"
// @Success      200  {array}   Entry
// @Failure      400  {object}  common.ErrorResponse  "A filter that cannot be parsed"
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /audit [get]
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter, err := parseFilter(q)
	if err != nil {
		common.HandlerError(w, common.NewAppError(http.StatusBadRequest, err.Error()))
		return
	}

	entries, err := h.service.Recent(r.Context(), normalizeLimit(q.Get("limit")), filter)
	if err != nil {
		common.HandlerError(w, err)
		return
	}

	common.WriteJSON(w, http.StatusOK, entries)
}

func normalizeLimit(limitParam string) int {
	limit, err := strconv.Atoi(limitParam)
	if err != nil || limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return limit
}
