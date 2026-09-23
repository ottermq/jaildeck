package main

import (
	"fmt"
	"log"
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
			if err := runPasswd(os.Args, os.Stdin, os.Stdout, cfg.StateDir); err != nil {
				fmt.Fprintln(os.Stderr, "jaildeck passwd:", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %q\n", os.Args[1])
			os.Exit(2)
		}
	}

	cfg := config.LoadConfig()
	a, err := app.New(cfg)
	if err != nil {
		log.Fatal(err.Error())
	}

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	srv := newServer(addr, a.Routes())
	log.Printf("Jail Deck %s listening on %s", buildinfo.Version(), addr)

	err = srv.ListenAndServe()
	if err != nil {
		log.Fatal(err.Error())
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
