package vulns

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/common"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// Get answers the last audit.
//
// @Summary      Vulnerable packages on the host, in jails and in templates
// @Description  The last audit of every installed package against the host's vulnerability
// @Description  database, `/var/db/pkg/vuln.xml` (#230, JD-061): each vulnerable package, its
// @Description  version, the VuXML entries it matches with their CVEs, and the packages that
// @Description  depend on it. **Read from the last audit, never run by this request.** Jail
// @Description  Deck audits at startup, every hour and whenever the database changes. It never
// @Description  fetches the database: the daily security run does. A tree is `unknown` when
// @Description  its packages could not be listed or there is no database, never `clean`, and
// @Description  `database.stale` is a database over three days old, which finds nothing new.
// @Description  A jail's package list is what that jail's root lets it say, so this is an aid
// @Description  for jails kept honestly, not a detector of a compromised one.
// @Tags         vulnerabilities
// @Produce      json
// @Success      200  {object}  Snapshot
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /vulnerabilities [get]
func (h *Handler) Get(w http.ResponseWriter, _ *http.Request) {
	common.WriteJSON(w, http.StatusOK, h.service.Snapshot())
}

func RegisterRoutes(r chi.Router, h *Handler) {
	r.Get("/vulnerabilities", h.Get)
}
