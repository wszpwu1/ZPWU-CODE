package main

import (
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/wszpwu1/ZPWU-CODE/internal/agent"
	"github.com/wszpwu1/ZPWU-CODE/internal/config"
	"github.com/wszpwu1/ZPWU-CODE/internal/handlers"
)

// init registers MIME types that Go's built-in table does not know about.
// Without this, http.FileServer serves /manifest.webmanifest as text/plain
// (content-sniffed), and Chrome rejects the manifest, which silently breaks
// PWA installation ("Add to Home Screen" as an installed app).
func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

func main() {
	cfg := config.Load()

	// The scratch-based image has no shell/wget, so Docker's HEALTHCHECK runs
	// this binary against itself: /zpwu -healthcheck
	if len(os.Args) > 1 {
		switch strings.TrimPrefix(os.Args[1], "-") {
		case "healthcheck":
			os.Exit(healthcheck(cfg.Addr))
		case "version":
			fmt.Println("ZPWU-CODE")
			return
		}
	}

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
