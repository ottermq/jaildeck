package names

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/common"
)

// Suggestion is what GET /api/names/suggest answers.
type Suggestion struct {
	Name string `json:"name"`
}

type Handler struct{ suggester *Suggester }

func NewHandler(suggester *Suggester) *Handler { return &Handler{suggester: suggester} }

// Suggest answers a free name for a new jail or guest.
//
// @Summary      Suggest a name
// @Description  A name for a new jail or guest in Jail Deck's style: an adjective and an animal,
// @Description  `jolly-otter` (#148). It is free on this host when answered -- no jail, guest or
// @Description  template has it -- and nothing reserves it: creating something still takes the
// @Description  name the request gives, and nothing on the server names anything unasked.
// @Description
// @Description  About 17,000 pairs, so the first draw is nearly always free; a host on which 20
// @Description  draws find nothing gets a pair with a two-digit number, `amazing-spider-42`.
// @Tags         names
// @Produce      json
// @Success      200  {object}  Suggestion
// @Failure      401  {object}  common.ErrorResponse
// @Failure      500  {object}  common.ErrorResponse  "The host's jails, guests or templates could not be read"
// @Failure      503  {object}  common.ErrorResponse  "Nothing free turned up, numbered or not"
// @Security     BearerAuth
// @Router       /names/suggest [get]
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	name, err := h.suggester.Suggest(r.Context())
	if errors.Is(err, ErrNoName) {
		common.HandlerError(w, common.NewAppError(http.StatusServiceUnavailable,
			"no free name turned up; give one yourself"))
		return
	}
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, Suggestion{Name: name})
}

func RegisterNameRoutes(r chi.Router, h *Handler) {
	r.Get("/names/suggest", h.Suggest)
}
