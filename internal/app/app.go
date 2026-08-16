package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/system/freebsd"
	"github.com/ottermq/jaildeck/internal/views"
)

type App struct {
	jailHandler  *jails.JailHandler
	auditHandler *audit.AuditHandler
}

func New() *App {
	operationLogger := audit.NewFileLogger("jaildeck-audit.log")
	renderer, err := views.NewRenderer()
	if err != nil {
		panic(err)
	}

	// jailSystem := system.NewFakeJailSystem()
	jailSystem := freebsd.NewAdapter(system.NewExecCommandRunner())
	jailService := jails.NewJailService(jailSystem, operationLogger)
	jailHandler := jails.NewJailHandler(jailService, renderer)

	operationService := audit.NewOperationService(operationLogger)
	operationHandler := audit.NewOperationHandler(operationService, renderer)

	return &App{
		jailHandler:  jailHandler,
		auditHandler: operationHandler,
	}
}

func (a *App) Routes() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/jails", http.StatusSeeOther)
	})

	r.Route("/jails", func(r chi.Router) {

		r.Get("/", a.jailHandler.List)

		r.Route("/{name}", func(r chi.Router) {
			r.Post("/start", a.jailHandler.Start)
			r.Post("/stop", a.jailHandler.Stop)
			r.Post("/restart", a.jailHandler.Restart)
		})
	})

	r.Route("/operations", func(r chi.Router) {
		r.Get("/", a.auditHandler.List)
	})

	return r
}
