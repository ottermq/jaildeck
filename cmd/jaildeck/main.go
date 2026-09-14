package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

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

	ip := cfg.Host
	addr := net.JoinHostPort(ip, cfg.Port)
	log.Printf("Jail Deck %s listening on %s", buildinfo.Version(), addr)

	err = http.ListenAndServe(addr, a.Routes())
	if err != nil {
		log.Fatal(err.Error())
	}
}
