package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/app"
	"github.com/ottermq/jaildeck/internal/buildinfo"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/web"
)

// @title Jail Deck API
// @version 0.7.0
// @description The JSON API a browser and an automation client both drive (JD-016).
// @description
// @description Every endpoint lives under /api. Authenticate either with the session
// @description cookie POST /api/auth/login sets, or with a bearer token created by
// @description `jaildeck token add <name>`. The Authorization header takes precedence
// @description over the cookie, and a bad token returns 401 rather than falling back to
// @description one, so a token client fails cleanly even while a session is active.
// @description
// @description Swagger 2.0 cannot describe a cookie as a security scheme, so only the
// @description bearer token appears below. The cookie works on every endpoint the token
// @description does.
// @description
// @description The version here is the API's and is bumped with the changelog at
// @description release time; it is not read from the binary.
//
// @BasePath /api
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer " followed by a token from `jaildeck token add`.
func main() {

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			// --commit is what packaging checks the binary against (#165).
			if len(os.Args) > 2 && os.Args[2] == "--commit" {
				revision, _ := buildinfo.Commit()
				fmt.Println(revision)
				return
			}
			fmt.Println(buildinfo.Version())
			return
		case "passwd":
			cfg := config.LoadConfig()
			// TODO: fix runPasswd to take subArgs (slice of os.Args) and fix the necessary tests
			if err := runPasswd(os.Args, os.Stdin, os.Stdout, cfg.StateDir); err != nil {
				fmt.Fprintln(os.Stderr, "jaildeck passwd:", err)
				os.Exit(1)
			}
			return
		case "init":
			cfg := config.LoadConfig()
			if err := runInit(os.Args[2:], os.Stdout, os.Stderr, cfg); err != nil {
				fmt.Fprintln(os.Stderr, "jaildeck init:", err)
				os.Exit(1)
			}
			return
		case "token":
			cfg := config.LoadConfig()
			if err := runToken(os.Args[2:], os.Stdout, os.Stderr, cfg.StateDir); err != nil {
				fmt.Fprintln(os.Stderr, "jaildeck token:", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %q\n", os.Args[1])
			os.Exit(2)
		}
	}

	cfg := config.LoadConfig()
	lvl, err := parseLogLevel(cfg.LogLevel)
	setupLogHandler(lvl)
	if err != nil {
		slog.Warn("unknown log level, defaulting to info", "value", cfg.LogLevel)
	}

	if cfg.Socket == "" && cfg.SocketOnly {
		slog.Error("JAILDECK_SOCKET_ONLY is set but JAILDECK_SOCKET names no socket, so there would be nothing to listen on")
		os.Exit(1)
	}
	// Only warn about the bind that is actually going to happen.
	if !cfg.SocketOnly {
		if warning := config.BindWarning(cfg.Host); warning != "" {
			slog.Warn(warning)
		}
	}
	a, err := app.New(cfg)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	srv := newServer(addr, a.Routes(web.Assets()))

	listeners, err := openListeners(cfg)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
	slog.Info("listening", "version", buildinfo.Version(), "on", listenerNames(listeners))

	// One http.Server over both listeners, so they share the handler and the
	// timeouts and cannot drift apart (#13). The first to fail ends the process:
	// a half-listening Jail Deck is harder to notice than one that exited.
	failed := make(chan error, len(listeners))
	for _, l := range listeners {
		go func(l net.Listener) { failed <- srv.Serve(l) }(l)
	}
	err = <-failed
	// Only reached when Serve itself returns. Jail Deck installs no signal
	// handler, so a SIGTERM from `service jaildeck stop` skips this entirely and
	// the socket file survives -- which is why clearStaleSocket exists and is
	// the path that actually runs after a restart, rather than this one.
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

// openListeners returns every listener the config asks for, closing the ones it
// already opened if a later one fails.
func openListeners(cfg *config.Config) ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, 2)
	if !cfg.SocketOnly {
		l, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, cfg.Port))
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, l)
	}
	if cfg.Socket != "" {
		l, err := listenUnix(cfg.Socket, cfg.SocketGroup)
		if err != nil {
			closeListeners(listeners)
			return nil, err
		}
		listeners = append(listeners, l)
	}
	if len(listeners) == 0 {
		return nil, errors.New("no listener configured")
	}
	return listeners, nil
}

func closeListeners(listeners []net.Listener) {
	for _, l := range listeners {
		_ = l.Close()
	}
}

func listenerNames(listeners []net.Listener) string {
	names := make([]string, 0, len(listeners))
	for _, l := range listeners {
		names = append(names, l.Addr().Network()+":"+l.Addr().String())
	}
	return strings.Join(names, " ")
}

func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is deliberately unset. It would cover handler execution,
		// and a jail operation may legitimately run to the 150s backstop
		// (JD-014) — those are detached from client cancellation, so a shorter
		// timeout would disconnect the operator while the jail kept changing.
		// Command timeouts already bound how long a handler can run.
	}
}

func setupLogHandler(lvl slog.Level) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: lvl,
		// syslog timestamps already (daemon(8) -S; drop ours.
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
}

func parseLogLevel(levelStr string) (slog.Level, error) {
	var lvl slog.Level
	err := lvl.UnmarshalText([]byte(levelStr))
	if err != nil {
		lvl = slog.LevelInfo
	}
	return lvl, err
}
