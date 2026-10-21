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
	"github.com/ottermq/jaildeck/internal/jails"
)

func (a *App) Routes(assets fs.FS) http.Handler {
	r := chi.NewRouter()

	useObservability(r)
	r.Route("/api", func(r chi.Router) {
		r.NotFound(apiNotFound)
		auth.RegisterAuthRoutes(r, a.authHandler)
		r.Group(func(r chi.Router) {
			r.Use(a.authHandler.RequireAuth)
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/api/jails", http.StatusSeeOther)
			})
			jails.RegisterJailRoutes(r, a.jailHandler)
			audit.RegisterAuditHandlers(r, a.auditHandler)
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
	// Every handler below gets a request-scoped logger; RequireAuth enriches it.
	r.Use(requestContextLogger)
}

func requestContextLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := slog.Default().With("method", r.Method, "path", r.URL.Path)
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
