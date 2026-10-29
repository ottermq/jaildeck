// Package about says which Jail Deck is running (#136): what the sidebar's
// badge and the Host page's block show.
package about

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/buildinfo"
	"github.com/ottermq/jaildeck/internal/common"
)

// About is what GET /api/about answers.
type About struct {
	// Version is what `jaildeck version` prints: "v0.19.0" for a release
	// or package build, the toolchain's stamp or "dev" otherwise.
	Version string `json:"version"`
	// Commit is the git revision the binary was built from, empty when the
	// toolchain stamped none, and Modified whether that tree had changes.
	Commit   string `json:"commit"`
	Modified bool   `json:"modified"`
	// Started is when this process started serving.
	Started time.Time `json:"started"`
	// StateDir is where the credential, the tokens and the sessions live
	// (JAILDECK_STATE_DIR).
	StateDir string `json:"state_dir"`
}

type Handler struct{ about About }

// NewHandler fixes what the answer says once, at startup: none of it changes
// while the process runs.
func NewHandler(started time.Time, stateDir string) *Handler {
	commit, modified := buildinfo.Commit()
	return &Handler{about: About{Version: buildinfo.Version(), Commit: commit, Modified: modified,
		Started: started, StateDir: stateDir}}
}

// Get answers which Jail Deck this is.
//
// @Summary      Which Jail Deck is running
// @Description  Its version as `jaildeck version` prints it, the commit it was built from, when
// @Description  it started and its state directory (#136). **Behind authentication**, like
// @Description  everything else: telling an unauthenticated visitor the exact version would
// @Description  make it easier to aim at a known issue.
// @Tags         about
// @Produce      json
// @Success      200  {object}  About
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /about [get]
func (h *Handler) Get(w http.ResponseWriter, _ *http.Request) {
	common.WriteJSON(w, http.StatusOK, h.about)
}

func RegisterAboutRoutes(r chi.Router, h *Handler) {
	r.Get("/about", h.Get)
}
