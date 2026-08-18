package app

import (
	"net/http"

	"codeberg.org/Otter-Labs/jaildeck/internal/audit"
	"codeberg.org/Otter-Labs/jaildeck/internal/jails"
	"codeberg.org/Otter-Labs/jaildeck/internal/system"
	"codeberg.org/Otter-Labs/jaildeck/internal/system/freebsd"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type App struct {
	jailHandler  *jails.JailHandler
	auditHandler *audit.AuditHandler
}

func New() *App {
	auditLogger := audit.NewFileLogger("jaildeck-audit.log")

	// jailSystem := fake.NewJailSystem()
	jailSystem := freebsd.NewAdapter(system.NewExecCommandRunner())
	jailService := jails.NewJailService(jailSystem, auditLogger)
	jailHandler := jails.NewJailHandler(jailService)

	auditService := audit.NewAuditService(auditLogger)
	auditHandler := audit.NewAuditHandler(auditService)

	return &App{
		jailHandler:  jailHandler,
		auditHandler: auditHandler,
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
