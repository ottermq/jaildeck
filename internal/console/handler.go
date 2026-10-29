package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/common"
)

// heartbeat is how often a session pings the page and looks again at its
// login and its idleness.
const heartbeat = 30 * time.Second

// Handler bridges a WebSocket to a session's terminal (JD-042): binary frames
// are the terminal's bytes both ways, text frames are control messages.
type Handler struct {
	service *Service
	// recheck answers whether the request's login still holds: a session
	// ends when the login that opened it expires or is revoked.
	recheck func(*http.Request) error
	// principal names who opened the session, for the audit log.
	principal func(*http.Request) string
	idle      time.Duration
	// origins are the extra hosts a page may connect from, past the
	// library's own check that the Origin is the Host.
	origins []string
	tick    time.Duration
}

// NewHandler takes the idle timeout, which output and pings do not reset:
// only what the operator types does.
func NewHandler(service *Service, recheck func(*http.Request) error, principal func(*http.Request) string,
	idle time.Duration, origins []string) *Handler {
	return &Handler{service: service, recheck: recheck, principal: principal, idle: idle,
		origins: origins, tick: heartbeat}
}

// control is a text frame. From the page: resize. From the server, last of
// all: closed, with why.
type control struct {
	Type   string `json:"type"`
	Cols   uint16 `json:"cols,omitempty"`
	Rows   uint16 `json:"rows,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Detail says more than the reason can, when there is more to say:
	// which process shares the console, and how to close it.
	Detail string `json:"detail,omitempty"`
}

// Jail opens a shell in a running jail.
//
// @Summary      A shell in a jail, over a WebSocket
// @Description  Upgrades to a WebSocket attached to `jexec -l <name> /bin/sh -l` on a PTY
// @Description  (JD-042). **Binary frames** carry the terminal's bytes, both ways. **Text frames**
// @Description  carry JSON: `{"type":"resize","cols":120,"rows":40}` from the client, and
// @Description  `{"type":"closed","reason":"..."}` from the server just before it closes.
// @Description
// @Description  `cols` and `rows` in the query are the terminal's size to start with (80×24 if
// @Description  absent). The session ends when the client goes, the shell exits, the jail
// @Description  stops, the login that opened it expires or is revoked, or nothing has been
// @Description  typed for the idle timeout (`JAILDECK_CONSOLE_IDLE`); the closing reason is one
// @Description  of `client`, `shell-exited`, `jail-stopped`, `session-expired`, `idle`.
// @Description  Stopping, restarting or destroying the jail hangs its consoles up first, so the
// @Description  stop does not wait on them. When the service itself stops, its shells get the
// @Description  kernel's hang-up and end, with no `console-close` entry.
// @Description
// @Description  Audited as `console-open` and `console-close`, with who opened it, how long it
// @Description  lasted and why it ended. **Nothing typed or printed is recorded.** The
// @Description  `Origin` must be this host's. A browser authenticates with its session cookie;
// @Description  an API token works for clients that can send the header.
// @Tags         jails
// @Param        name  path   string  true   "Jail name"
// @Param        cols  query  int     false  "Columns, 1 to 1000"
// @Param        rows  query  int     false  "Rows, 1 to 1000"
// @Success      101  "Switching protocols"
// @Failure      400  {object}  common.ErrorResponse  "Not a WebSocket request, or a size out of range"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "No such jail"
// @Failure      409  {object}  common.ErrorResponse  "The jail is not running"
// @Security     BearerAuth
// @Router       /jails/{name}/console [get]
func (h *Handler) Jail(w http.ResponseWriter, r *http.Request) {
	cols, rows, err := size(r)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	if !isUpgrade(r) {
		common.HandlerError(w, common.NewAppError(http.StatusBadRequest,
			"this is a WebSocket endpoint; connect with a WebSocket client"))
		return
	}
	// Before anything opens: a page from another origin carries the
	// operator's cookie too, and must not get as far as starting a shell.
	if err := h.checkOrigin(r); err != nil {
		common.HandlerError(w, err)
		return
	}
	session, err := h.service.OpenJail(r.Context(), chi.URLParam(r, "name"), h.principal(r), cols, rows)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	h.serve(w, r, session)
}

// VM attaches to a running guest's serial console.
//
// @Summary      A guest's serial console, over a WebSocket
// @Description  Upgrades to a WebSocket attached to the guest's first console port: the host's
// @Description  end of its nmdm pair, as `vm info` names it, made raw before anything passes
// @Description  (JD-042). Frames as for a jail's console: binary for the terminal's bytes, text
// @Description  for JSON control. A serial line has no size, so `resize` changes nothing; the
// @Description  guest's own `stty` or `resize` does.
// @Description
// @Description  **One session per guest**: a second is refused with `409`. An outside
// @Description  `vm console` or `cu` on the host can still attach at the same time; FreeBSD lets
// @Description  root past an exclusive lock, measured. **Closing the console does not log anyone
// @Description  out of the guest**: a login on the serial line stays logged in.
// @Description
// @Description  **Another reader on the device** -- a `vm console` left open in a shell -- would get
// @Description  half of every line the guest prints, and this console the other half. The
// @Description  connection is then accepted and closed at once with `{"type":"closed","reason":
// @Description  "console-shared","detail":"..."}` naming the process, rather than refused: a
// @Description  browser cannot read a refused handshake. One that appears during a session ends it
// @Description  the same way.
// @Description
// @Description  The session ends as a jail's does, and also with `guest-stopped` when the guest
// @Description  is no longer running (looked at every 5 s: a powered-off guest gives the device's
// @Description  reader nothing at all), or `console-closed` when the device ends. Audited as
// @Description  `console-open` and `console-close`; nothing typed or printed is recorded.
// @Tags         vms
// @Param        name  path  string  true  "Guest name"
// @Success      101  "Switching protocols"
// @Failure      400  {object}  common.ErrorResponse  "Not a WebSocket request"
// @Failure      401  {object}  common.ErrorResponse
// @Failure      404  {object}  common.ErrorResponse  "No such guest"
// @Failure      409  {object}  common.ErrorResponse  "The guest is not running, or a console is open on it already"
// @Security     BearerAuth
// @Router       /vms/{name}/console [get]
func (h *Handler) VM(w http.ResponseWriter, r *http.Request) {
	if !isUpgrade(r) {
		common.HandlerError(w, common.NewAppError(http.StatusBadRequest,
			"this is a WebSocket endpoint; connect with a WebSocket client"))
		return
	}
	if err := h.checkOrigin(r); err != nil {
		common.HandlerError(w, err)
		return
	}
	session, err := h.service.OpenVM(r.Context(), chi.URLParam(r, "name"), h.principal(r))
	var shared *SharedError
	if errors.As(err, &shared) {
		h.refuse(w, r, ReasonConsoleShared, shared.Message)
		return
	}
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	h.serve(w, r, session)
}

// refuse accepts the connection only to close it at once with a reason and
// what to do: the one refusal a page must be able to read (#153).
func (h *Handler) refuse(w http.ResponseWriter, r *http.Request, reason, detail string) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.origins})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if closing, err := json.Marshal(control{Type: "closed", Reason: reason, Detail: detail}); err == nil {
		_ = conn.Write(ctx, websocket.MessageText, closing)
	}
	_ = conn.Close(websocket.StatusPolicyViolation, reason)
}

// serve upgrades the request and runs the session until something ends it.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, session *Session) {
	// The server's ReadTimeout sets a deadline on the connection, and
	// net/http says a hijacked connection "may have read or write deadlines
	// already set". Go 1.26 happens to clear the read one while hijacking (the
	// test passes without these lines); clearing them here keeps a console
	// from depending on that and dropping after 30 seconds.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.origins})
	if err != nil {
		// Accept has answered the client already.
		h.service.Finish(r.Context(), session, ReasonClient)
		return
	}
	conn.SetReadLimit(64 << 10)
	reason := h.bridge(r, conn, session)
	h.service.Finish(r.Context(), session, reason)
	reason = session.Reason()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if closing, err := json.Marshal(control{Type: "closed", Reason: reason}); err == nil {
		_ = conn.Write(ctx, websocket.MessageText, closing)
	}
	_ = conn.Close(websocket.StatusNormalClosure, reason)
}

// bridge copies both ways until one side ends, and answers why.
func (h *Handler) bridge(r *http.Request, conn *websocket.Conn, session *Session) string {
	// Not cancelled when the bridge returns: the library closes the
	// connection when a read's context is cancelled, and the closed message
	// still has to go out. conn.Close is what ends the read.
	ctx := r.Context()

	ended := make(chan string, 4)
	stop := func(reason string) {
		select {
		case ended <- reason:
		default:
		}
	}
	var mu sync.Mutex
	typed := time.Now()

	// The shell's output to the page.
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					stop(ReasonClient)
					return
				}
			}
			if err != nil {
				stop(session.OutputEnded())
				return
			}
		}
	}()

	// The page's input to the shell.
	go func() {
		for {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				stop(ReasonClient)
				return
			}
			if kind == websocket.MessageBinary {
				mu.Lock()
				typed = time.Now()
				mu.Unlock()
				if _, err := session.Write(data); err != nil {
					stop(session.OutputEnded())
					return
				}
				continue
			}
			var message control
			if json.Unmarshal(data, &message) == nil && message.Type == "resize" &&
				validSize(message.Cols) && validSize(message.Rows) {
				_ = session.Resize(message.Cols, message.Rows)
			}
		}
	}()

	ticker := time.NewTicker(h.tick)
	defer ticker.Stop()
	for {
		select {
		case reason := <-ended:
			return reason
		case <-session.Ended():
			return session.Reason()
		case <-ticker.C:
			mu.Lock()
			quiet := time.Since(typed)
			mu.Unlock()
			if h.idle > 0 && quiet >= h.idle {
				return ReasonIdle
			}
			if h.recheck != nil && h.recheck(r) != nil {
				return ReasonSessionExpired
			}
			pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return ReasonClient
			}
		}
	}
}

// checkOrigin is the library's own Origin check, made before the shell starts
// rather than at the upgrade: no Origin (not a browser) passes, the request's
// own Host passes, and so does a host matching one of the configured
// patterns.
func (h *Handler) checkOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err == nil && u.Host != "" {
		if strings.EqualFold(u.Host, r.Host) {
			return nil
		}
		for _, pattern := range h.origins {
			target := u.Host
			if strings.Contains(pattern, "://") {
				target = u.Scheme + "://" + u.Host
			}
			if ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(target)); ok {
				return nil
			}
		}
	}
	return common.NewAppError(http.StatusForbidden, fmt.Sprintf(
		"a console can't be opened from %s; it is this host's own pages, or JAILDECK_CONSOLE_ORIGINS", strconv.Quote(origin)))
}

func isUpgrade(r *http.Request) bool {
	return r.Header.Get("Upgrade") != "" || r.Header.Get("Sec-WebSocket-Key") != ""
}

func size(r *http.Request) (cols, rows uint16, err error) {
	cols, rows = 80, 24
	for _, q := range []struct {
		name string
		into *uint16
	}{{"cols", &cols}, {"rows", &rows}} {
		value := r.URL.Query().Get(q.name)
		if value == "" {
			continue
		}
		n, convErr := strconv.ParseUint(value, 10, 16)
		if convErr != nil || !validSize(uint16(n)) {
			return 0, 0, common.NewAppError(http.StatusBadRequest,
				q.name+" is "+strconv.Quote(value)+"; give a number from 1 to 1000")
		}
		*q.into = uint16(n)
	}
	return cols, rows, nil
}

func validSize(n uint16) bool { return n >= 1 && n <= 1000 }
