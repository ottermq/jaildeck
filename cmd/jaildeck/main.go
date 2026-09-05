package main

import (
	"fmt"
	"log"
	"net/http"

	"codeberg.org/Otter-Labs/jaildeck/internal/app"
	"codeberg.org/Otter-Labs/jaildeck/internal/config"
)

func main() {
	cfg := config.LoadConfig()

	a, err := app.New(cfg)
	if err != nil {
		log.Fatal(err.Error())
	}

	ip := cfg.Host
	if ip == "" {
		ip = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%s", ip, cfg.Port)
	log.Print("Jail Deck listening on " + addr)

	err = http.ListenAndServe(addr, a.Routes())
	if err != nil {
		log.Fatal(err.Error())
	}
}
