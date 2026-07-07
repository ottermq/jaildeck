package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/ottermq/jaildeck/internal/app"
	"github.com/ottermq/jaildeck/internal/config"
)

func main() {
	cfg := config.LoadConfig()

	a := app.New()
	ip := cfg.Host
	if ip == "" {
		ip = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%s", ip, cfg.Port)
	log.Print("Jail Deck listening on " + addr)

	err := http.ListenAndServe(addr, a.Routes())
	if err != nil {
		log.Fatal(err.Error())
	}
}
