package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"codeberg.org/Otter-Labs/jaildeck/internal/app"
	"codeberg.org/Otter-Labs/jaildeck/internal/buildinfo"
	"codeberg.org/Otter-Labs/jaildeck/internal/config"
)

func main() {

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
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

	warning := config.BindWarning(cfg.Host)
	if warning != "" {
		slog.Warn(warning)
	}
	a, err := app.New(cfg)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	srv := newServer(addr, a.Routes())
	slog.Info(fmt.Sprintf("Jail Deck %s listening on %s", buildinfo.Version(), addr))

	err = srv.ListenAndServe()
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
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
