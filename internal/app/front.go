package app

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/state"
)

// Front is the unprivileged side of Jail Deck (JD-056): the listener, the
// UI, signing in and authenticating. Everything it authenticates goes to the
// root side, with the principal named in a header. It is the only side that
// reads the state directory, and the only one that talks to the network.
type Front struct {
	authHandler *auth.AuthHandler
	backend     http.Handler
}

// NewFront builds the front. dial reaches the root side's socket.
func NewFront(cfg *config.Config, dial func(ctx context.Context) (net.Conn, error)) (*Front, error) {
	store := state.NewStore(cfg.StateDir)
	if err := requireCredential(store); err != nil {
		return nil, err
	}
	authHandler := auth.NewAuthHandler(auth.NewAuthService(store, auth.NewSessionStore()))
	if cfg.NoConsoleUI {
		authHandler.SetSessionWithout(auth.ScopeConsole)
	}
	return &Front{authHandler: authHandler, backend: newBackendProxy(dial)}, nil
}

// Routes is what the front serves to clients: signing in itself, the UI,
// and every other API route and /metrics passed to the root side once
// authenticated.
func (f *Front) Routes(assets fs.FS) http.Handler {
	r := chi.NewRouter()

	useObservability(r)
	r.Route("/api", func(r chi.Router) {
		r.NotFound(apiNotFound)
		auth.RegisterAuthRoutes(r, f.authHandler)
		r.With(f.authHandler.RequireAuth).Handle("/*", f.backend)
	})
	r.With(f.authHandler.RequireAuth).Handle("/metrics", f.backend)

	// Serve SPA fallback handler for any unmatched paths (handles frontend routing)
	r.NotFound(staticHandler(assets))

	return r
}

// RecheckRoutes is what the front serves on its own socket, to the root side
// alone: whether a request's login still holds.
func (f *Front) RecheckRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /recheck", f.authHandler.RecheckHandler())
	return mux
}

// newBackendProxy passes a request to the root side as it came, with the
// principal added. The path goes as it was escaped, since Restrict refuses a
// limited token's escaped path (JD-055) and would otherwise never see one.
func newBackendProxy(dial func(ctx context.Context) (net.Conn, error)) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "jaildeck"
			// The console's origin check compares Origin with the Host the
			// browser used.
			pr.Out.Host = pr.In.Host
			auth.ForwardPrincipal(pr.In.Context(), pr.Out.Header)
		},
		Transport: &http.Transport{
			DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
		},
		// The event stream is written as it happens, not when a buffer fills.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			common.Logger(r.Context(), "http").Error("the root side did not answer", slog.Any("err", err))
			common.HandlerError(w, common.NewAppError(http.StatusBadGateway,
				"Jail Deck's root side is not answering; its log says why"))
		},
	}
}

// App is both sides in one process, joined by in-memory connections instead
// of sockets: for the fake adapter, a development box, and a host that runs
// Jail Deck without a user of its own (JD-056). Every request still crosses
// the same boundary.
type App struct {
	front *Front
	back  *Back
}

func New(cfg *config.Config) (*App, error) {
	toBack, toFront := newPipeListener(), newPipeListener()
	front, err := NewFront(cfg, toBack.Dial)
	if err != nil {
		return nil, err
	}
	back, err := NewBack(cfg, auth.RemoteRecheck(&http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return toFront.Dial(ctx) },
	}}))
	if err != nil {
		return nil, err
	}
	go servePipe(toBack, back.Routes())
	go servePipe(toFront, front.RecheckRoutes())
	return &App{front: front, back: back}, nil
}

func (a *App) Routes(assets fs.FS) http.Handler {
	return a.front.Routes(assets)
}

func servePipe(l net.Listener, h http.Handler) {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(l); err != nil {
		slog.Error("an in-process side stopped", slog.Any("err", err))
	}
}

// pipeListener is a net.Listener whose connections are net.Pipe pairs, made
// by Dial.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "in-process" }
