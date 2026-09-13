package main

import (
	"log"
	"net/http"

	"github.com/wszpwu1/ZPWU-CODE/internal/agent"
	"github.com/wszpwu1/ZPWU-CODE/internal/config"
	"github.com/wszpwu1/ZPWU-CODE/internal/handlers"
)

func main() {
	cfg := config.Load()

	// Apply the SSRF policy for user-supplied LLM base_url values before any
	// request can reach NormalizeOpenAIEndpoint / callClaude.
	agent.SetSSRFStrict(cfg.SSRFStrict)

	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux, cfg)

	fs := http.FileServer(http.Dir("web"))
	mux.Handle("/", fs)

	log.Printf("server listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
