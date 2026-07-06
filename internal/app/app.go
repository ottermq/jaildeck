package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/otterlabs/jaildeck/internal/handlers"
	"github.com/otterlabs/jaildeck/internal/services"
	"github.com/otterlabs/jaildeck/internal/system"
	"github.com/otterlabs/jaildeck/internal/system/freebsd"
	"github.com/otterlabs/jaildeck/internal/views"
)

type App struct {
	jailHandler *handlers.JailHandler
}

func New() *App {
	renderer, err := views.NewRenderer()
	if err != nil {
		panic(err)
	}

	// jailSystem := system.NewFakeJailSystem()
	jailSystem := freebsd.NewAdapter(system.NewExecCommandRunner())
	jailService := services.NewJailService(jailSystem)
	jailHandler := handlers.NewJailHandler(jailService, renderer)

	return &App{
		jailHandler: jailHandler,
	}
}

func (a *App) Routes() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

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

	return r
}
