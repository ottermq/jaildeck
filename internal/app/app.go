package app

import (
	"fmt"
	"net/http"

	"codeberg.org/Otter-Labs/jaildeck/internal/audit"
	"codeberg.org/Otter-Labs/jaildeck/internal/config"
	"codeberg.org/Otter-Labs/jaildeck/internal/jails"
	"codeberg.org/Otter-Labs/jaildeck/internal/system"
	"codeberg.org/Otter-Labs/jaildeck/internal/system/fake"
	"codeberg.org/Otter-Labs/jaildeck/internal/system/freebsd"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type App struct {
	jailHandler  *jails.JailHandler
	auditHandler *audit.AuditHandler
}

func New(cfg *config.Config) (*App, error) {
	auditLogger := audit.NewFileLogger("jaildeck-audit.log")

	jailSystem, err := newJailSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}

	jailService := jails.NewJailService(jailSystem, auditLogger)
	jailHandler := jails.NewJailHandler(jailService)

	auditService := audit.NewAuditService(auditLogger)
	auditHandler := audit.NewAuditHandler(auditService)

	return &App{
		jailHandler:  jailHandler,
		auditHandler: auditHandler,
	}, nil
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

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/jails", http.StatusSeeOther)
	})

	jails.RegisterJailRoutes(r, a.jailHandler)

	audit.RegisterAuditHandlers(r, a.auditHandler)

	return r
}
