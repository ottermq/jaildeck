package app

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/metrics"
	"github.com/ottermq/jaildeck/internal/storage"
	"github.com/ottermq/jaildeck/internal/templates"
)

func (a *App) Routes(assets fs.FS) http.Handler {
	r := chi.NewRouter()

	useObservability(r)
	r.Route("/api", func(r chi.Router) {
		r.NotFound(apiNotFound)
		auth.RegisterAuthRoutes(r, a.authHandler)
		r.Group(func(r chi.Router) {
			r.Use(a.authHandler.RequireAuth)
			r.Get("/", a.apiRoot)
			jails.RegisterJailRoutes(r, a.jailHandler)
			host.RegisterHostRoutes(r, a.hostHandler)
			audit.RegisterAuditRoutes(r, a.auditHandler)
			metrics.RegisterMetricsRoutes(r, a.metricsHandler)
			storage.RegisterStorageRoutes(r, a.storageHandler)
			templates.RegisterTemplateRoutes(r, a.templateHandler)
		})
	})

	// Serve SPA fallback handler for any unmatched paths (handles frontend routing)
	r.NotFound(staticHandler(assets))

	return r
}

// useObservability registers the two middlewares whose order matters.
func useObservability(r *chi.Mux) {
	// RequestLogger before Recoverer deliberately: Recoverer calls
	// GetLogEntry(r).Panic(...) itself, so the entry must already be in the
	// context. Reversed, chi falls back to PrintPrettyStack on stdout.
	r.Use(middleware.RequestLogger(slogFormatter{}))
	r.Use(middleware.Recoverer)
	// requestID before requestContextLogger, so the logger carries the id too
	// and the access log can be lined up against the audit entries.
	r.Use(requestID)
	// Every handler below gets a request-scoped logger; RequireAuth enriches it.
	r.Use(requestContextLogger)
}

// requestID puts a validated X-Request-Id where the audit logger will find it
// (JD-016). An invalid one is DROPPED and logged, not rejected: the id is a
// correlation convenience, and failing an operator's jail restart because their
// correlation id held a space would be the worse outcome. The audit entry then
// simply carries none, which is the same shape as a client that sent none.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get(audit.RequestIDHeader)
		id, err := audit.ValidateRequestID(raw)
		if err != nil {
			// slog's handler quotes a value that needs it, so a header holding
			// a newline cannot forge a log line here.
			common.Logger(r.Context(), "http").Warn("ignoring an invalid request id",
				slog.String("reason", err.Error()), slog.String("value", raw))
		}
		if id != "" {
			r = r.WithContext(audit.ContextWithRequestID(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}

func requestContextLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := slog.Default().With("method", r.Method, "path", r.URL.Path)
		if id := audit.RequestIDFromContext(r.Context()); id != "" {
			l = l.With("request_id", id)
		}
		next.ServeHTTP(w, r.WithContext(common.ContextWithLogger(r.Context(), l)))
	})
}

// notBuilt is served when the frontend has not been built. A fresh clone
// compiles and runs with Go alone (JD-015), so an absent index.html is an
// expected state rather than a failure.
const notBuilt = `<!doctype html><meta charset="utf-8"><title>Jail Deck</title>
<p>The frontend has not been built. Run <code>make web</code>.</p>`

func staticHandler(distFS fs.FS) http.HandlerFunc {
	index, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		index = []byte(notBuilt)
	}

	fileServer := http.FileServer(http.FS(distFS))

	return func(w http.ResponseWriter, r *http.Request) {
		urlPath := path.Clean(r.URL.Path)
		if urlPath == "/" || urlPath == "." {
			serveIndex(w, index)
			return
		}

		fsPath := strings.TrimPrefix(urlPath, "/")

		stat, err := fs.Stat(distFS, fsPath)
		if err != nil || stat.IsDir() {
			// Asset doesn't exist (or is a dir), treat it as an SPA routing path
			serveIndex(w, index)
			return
		}

		if strings.HasPrefix(fsPath, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		fileServer.ServeHTTP(w, r)
	}
}

func serveIndex(w http.ResponseWriter, content []byte) {
	// Explicitly prevent index.html from being cached so updates register immediately
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(content)
}

func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	common.HandlerError(w, common.NewAppError(http.StatusNotFound, "not found"))
}

// apiRoot sends a caller who guessed at the base path somewhere useful.
//
// A named method rather than the closure it used to be, so that the generated
// spec can describe it: swag parses annotations above functions, and an inline
// handler has nowhere to put them.
//
// @Summary      API root
// @Description  Redirects to /api/jails, so a client that guessed the base path lands on
// @Description  something rather than a 404. It is behind authentication like everything
// @Description  else here, so an unauthenticated caller gets 401 instead of the redirect.
// @Tags         meta
// @Success      303  "See Other: /api/jails"
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       / [get]
func (a *App) apiRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/api/jails", http.StatusSeeOther)
}
