package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ottermq/jaildeck/internal/handlers"
	"github.com/ottermq/jaildeck/internal/operations"
	"github.com/ottermq/jaildeck/internal/services"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/system/freebsd"
	"github.com/ottermq/jaildeck/internal/views"
)

type App struct {
	jailHandler      *handlers.JailHandler
	operationHandler *handlers.OperationHandler
}

func New() *App {
	operationLogger := operations.NewFileLogger("jaildeck-operations.log")
	renderer, err := views.NewRenderer()
	if err != nil {
		panic(err)
	}

	// jailSystem := system.NewFakeJailSystem()
	jailSystem := freebsd.NewAdapter(system.NewExecCommandRunner())
	jailService := services.NewJailService(jailSystem, operationLogger)
	jailHandler := handlers.NewJailHandler(jailService, renderer)

	operationService := services.NewOperationService(operationLogger)
	operationHandler := handlers.NewOperationHandler(operationService, renderer)

	return &App{
		jailHandler:      jailHandler,
		operationHandler: operationHandler,
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
		r.Get("/", a.operationHandler.List)
	})

	return r
}
