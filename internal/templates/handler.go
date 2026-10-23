package templates

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/common"
)

type TemplateHandler struct {
	service *Service
}

func NewTemplateHandler(service *Service) *TemplateHandler {
	return &TemplateHandler{service: service}
}

// deriveRequest is the body of PUT /api/templates/{name}.
type deriveRequest struct {
	// From is the template this one is derived from, by name.
	From string `json:"from"`
	// Packages are installed with pkg -c into the clone. Empty is allowed and
	// means a plain copy, which is a reasonable thing to want.
	Packages []string `json:"packages"`
}

// List returns every template on the host.
//
// @Summary      List templates
// @Description  Every template under <pool>/jails/templates, with whether it is usable and
// @Description  what it was derived from.
// @Description
// @Description  Nothing here is stored (JD-011). `ready` is whether the `@base` snapshot
// @Description  exists, which is what makes a template usable (JD-007), and `parent` is read
// @Description  back from `zfs get origin` rather than recorded — so a derived template
// @Description  cannot disagree with where it actually came from.
// @Description
// @Description  A template that exists and is **not** ready is a derive that failed part
// @Description  way through. It needs no cleanup to be truthful: it is simply unusable, and
// @Description  says so.
// @Tags         templates
// @Produce      json
// @Success      200  {array}   Template
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /templates [get]
func (h *TemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	found, err := h.service.List(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, found)
}

// Derive builds a template from another plus packages.
//
// @Summary      Derive a template
// @Description  A release template plus packages, re-snapshotted — a PostgreSQL template,
// @Description  say, so every postgres jail starts with it installed (#7, JD-027).
// @Description
// @Description  Three steps: clone the parent's `@base`, `pkg -c <root> install -y` the
// @Description  packages, snapshot the result as `@base`.
// @Description
// @Description  `pkg -c` chroots, so nothing is started: no jail, no stanza, no networking
// @Description  for a temporary jail. It is the same shape as JD-007's `freebsd-update -b`,
// @Description  which already runs against an unbooted template root.
// @Description
// @Description  **It installs packages and nothing else.** For anything further — `sysrc`, a
// @Description  configuration file, an `initdb` — configure a jail and clone it with
// @Description  `POST /api/jails/{name}/template` instead (JD-027).
// @Description
// @Description  **201** built · **200** it was already there and ready, so a client that
// @Description  timed out can repeat the call (JD-016).
// @Description
// @Description  **409 if it exists but is not ready.** That is what a previous failed
// @Description  derive leaves behind, and finishing it silently would install packages on
// @Description  top of whatever that attempt left. Destroy it or inspect it first.
// @Description
// @Description  A package name beginning with a dash is refused: `pkg` would read it as an
// @Description  option rather than a package, and `install -y -f` is not the command that
// @Description  was asked for. No shell is involved either way.
// @Description
// @Description  If `pkg` fails, the clone stays **without** its `@base` and the listing
// @Description  reports it as not ready. pkg's own message comes back, as a `400` when pkg
// @Description  ran and refused — a package that does not exist is the request's fault.
// @Tags         templates
// @Accept       json
// @Produce      json
// @Param        name     path      string         true  "The new template's name"
// @Param        derive   body      deriveRequest  true  "The parent and the packages"
// @Success      200      {object}  Template  "Already there and ready"
// @Success      201      {object}  Template  "Built"
// @Failure      400      {object}  common.ErrorResponse  "A bad name, or a package pkg refused"
// @Failure      401      {object}  common.ErrorResponse
// @Failure      404      {object}  common.ErrorResponse  "No such parent template"
// @Failure      409      {object}  common.ErrorResponse  "It exists but is not ready, or the parent is not"
// @Security     BearerAuth
// @Router       /templates/{name} [put]
func (h *TemplateHandler) Derive(w http.ResponseWriter, r *http.Request) {
	var body deriveRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		common.HandlerError(w, common.NewAppError(http.StatusBadRequest,
			"could not read the request body: "+err.Error()))
		return
	}

	template, built, err := h.service.Derive(r.Context(), chi.URLParam(r, "name"), body.From, body.Packages)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	if built {
		common.WriteJSON(w, http.StatusCreated, template)
		return
	}
	common.WriteJSON(w, http.StatusOK, template)
}

func RegisterTemplateRoutes(r chi.Router, h *TemplateHandler) {
	r.Route("/templates", func(r chi.Router) {
		r.Get("/", h.List)
		r.Put("/{name}", h.Derive)
	})
}
