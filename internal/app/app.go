package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"codeberg.org/Otter-Labs/jaildeck/internal/audit"
	"codeberg.org/Otter-Labs/jaildeck/internal/auth"
	"codeberg.org/Otter-Labs/jaildeck/internal/common"
	"codeberg.org/Otter-Labs/jaildeck/internal/config"
	"codeberg.org/Otter-Labs/jaildeck/internal/jails"
	"codeberg.org/Otter-Labs/jaildeck/internal/state"
	"codeberg.org/Otter-Labs/jaildeck/internal/system"
	"codeberg.org/Otter-Labs/jaildeck/internal/system/fake"
	"codeberg.org/Otter-Labs/jaildeck/internal/system/freebsd"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var ErrNoCredential = errors.New("no operator credential configured")

type App struct {
	authHandler  *auth.AuthHandler
	jailHandler  *jails.JailHandler
	auditHandler *audit.AuditHandler
}

func New(cfg *config.Config) (*App, error) {
	authState := state.NewStore(cfg.StateDir)
	if err := requireCredential(authState); err != nil {
		return nil, err
	}

	sessions := auth.NewSessionStore()
	authService := auth.NewAuthService(authState, sessions)
	authHandler := auth.NewAuthHandler(authService)

	auditLogger, err := setupAuditLog(cfg.AuditLog)
	if err != nil {
		return nil, err
	}

	jailSystem, err := newJailSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}

	jailService := jails.NewJailService(jailSystem, auditLogger)
	jailHandler := jails.NewJailHandler(jailService)

	auditService := audit.NewAuditService(auditLogger)
	auditHandler := audit.NewAuditHandler(auditService)

	return &App{
		authHandler:  authHandler,
		jailHandler:  jailHandler,
		auditHandler: auditHandler,
	}, nil
}

func requireCredential(store *state.Store) error {
	st, err := store.Load()
	switch {
	case errors.Is(err, state.ErrNotConfigured):
		return fmt.Errorf("%w in %s. Run: jaildeck passwd", ErrNoCredential, store.Dir())
	case err != nil:
		// corrupted or unreadable:
		// No need of dedicated sentinels
		// Load() already names the path and the cause, and this must not be reported as "run jaildeck passwd"
		return err
	case st.PasswordHash == "":
		return fmt.Errorf("%w in %s. Run: jaildeck passwd", ErrNoCredential, store.Dir())
	}
	return nil
}

func setupAuditLog(auditLogFilePath string) (*audit.FileLogger, error) {
	err := os.MkdirAll(filepath.Dir(auditLogFilePath), 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating audit log directory: %w", err)
	}
	f, err := os.OpenFile(auditLogFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening audit log: %w", err)
	}
	defer f.Close()

	auditLogger := audit.NewFileLogger(auditLogFilePath)
	return auditLogger, nil
}

// newJailSystem selects the jails.JailSystem implementation named by adapter.
// An unknown name is an error rather than a silent fallback: which adapter is
// running determines whether real jails are touched.
func newJailSystem(adapter string) (jails.JailSystem, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewJailSystem(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

func (a *App) Routes() http.Handler {
	r := chi.NewRouter()

	useObservability(r)
	r.Route("/api", func(r chi.Router) {
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
