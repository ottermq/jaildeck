package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/common"
)

// Heartbeat is how often a quiet stream says it is still there, and how
// often its credentials are checked again.
const Heartbeat = 15 * time.Second

type Handler struct {
	broker *Broker
	// recheck answers whether the request's credentials still hold: a
	// stream ends when its session expires or its token is revoked.
	recheck   func(*http.Request) error
	heartbeat time.Duration
}

func NewHandler(broker *Broker, recheck func(*http.Request) error) *Handler {
	return &Handler{broker: broker, recheck: recheck, heartbeat: Heartbeat}
}

func RegisterEventRoutes(r chi.Router, h *Handler) {
	r.Get("/events", h.Stream)
}

// Stream is the event stream.
//
// @Summary      Live events
// @Description  Server-sent events (`text/event-stream`) of what changes (#124, JD-052). Each event is a
// @Description  hint: read what it names again through the usual endpoints, which stay the contract.
// @Description
// @Description  - `operation`: an operation Jail Deck ran, as audited: `operation`, `target`, `success`,
// @Description    `request_id`, `timestamp`.
// @Description  - `state`: a jail or guest found in another state by the periodic comparison, made outside
// @Description    Jail Deck or by the guest itself: `kind`, `name`, `from`, `to` (`absent` when it appeared or
// @Description    went), `seen: observed`. Compared every 10 s while any stream is open.
// @Description  - `progress`: a template's or image's step: `kind`, `name`, `step`; an empty step is done.
// @Description  - `reset`: events were missed; read everything again.
// @Description
// @Description  Ids are `<boot>-<sequence>`. A reconnect with `Last-Event-ID` is sent what it missed while the
// @Description  last 256 events still hold it, and a `reset` otherwise. A comment every 15 s keeps a quiet
// @Description  stream open; the credentials are checked again then, and a stream whose session expired or
// @Description  token was revoked is closed. At most 32 streams at once.
// @Tags         events
// @Produce      text/event-stream
// @Success      200  {string}  string  "The stream"
// @Failure      401  {object}  common.ErrorResponse  "Not signed in"
// @Failure      503  {object}  common.ErrorResponse  "Too many streams"
// @Router       /events [get]
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		common.HandlerError(w, common.NewAppError(http.StatusInternalServerError, "this connection cannot stream"))
		return
	}
	sub, replay, reset, latest, err := h.broker.Subscribe(r.Header.Get("Last-Event-ID"))
	if errors.Is(err, ErrTooMany) {
		common.HandlerError(w, common.NewAppError(http.StatusServiceUnavailable, err.Error()))
		return
	}
	defer h.broker.Unsubscribe(sub)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\n")
	if reset {
		writeReset(w, latest)
	}
	for _, e := range replay {
		if err := write(w, e); err != nil {
			return
		}
	}
	flusher.Flush()

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := h.recheck(r); err != nil {
				common.Logger(r.Context(), "events").Info("closing an event stream", slog.Any("reason", err))
				return
			}
			if _, err := fmt.Fprint(w, ": still here\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e, open := <-sub.C:
			if !open {
				// Fell behind: told to reload, then closed, so the
				// reconnect starts clean.
				writeReset(w, h.latest())
				flusher.Flush()
				return
			}
			if err := write(w, e); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *Handler) latest() string {
	h.broker.mu.Lock()
	defer h.broker.mu.Unlock()
	return fmt.Sprintf("%s-%d", h.broker.boot, h.broker.seq)
}

func write(w io.Writer, e Event) error {
	data, err := json.Marshal(e.Data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", e.ID, e.Type, data)
	return err
}

func writeReset(w io.Writer, latest string) {
	fmt.Fprintf(w, "id: %s\nevent: %s\ndata: {}\n\n", latest, TypeReset)
}
