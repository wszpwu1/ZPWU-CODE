package main

import (
	"log"
	"net/http"

	"github.com/wszpwu1/ZPWU-CODE/internal/config"
	"github.com/wszpwu1/ZPWU-CODE/internal/handlers"
)

func main() {
	cfg := config.Load()

	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux, cfg)

	fs := http.FileServer(http.Dir("web"))
	mux.Handle("/", fs)

	log.Printf("server listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
