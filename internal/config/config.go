package config

import (
	"os"
	"strings"
)

type Config struct {
	Addr           string
	AccessToken    string // server-level guard token (optional, for self-hosted protection)
	GitHubClientID string
	GitHubSecret   string
	// PublicBaseURL is the absolute base URL (e.g. https://code.example.com)
	// used to build the OAuth redirect_uri. When empty the server falls back to
	// deriving it from the request's Host header. Set this behind a reverse proxy
	// so an attacker-controlled Host header cannot alter the OAuth redirect target.
	PublicBaseURL string
	// SSRFStrict additionally blocks loopback and private (RFC1918) hosts for
	// user-supplied LLM base_url values. Leave false for local/self-hosted
	// deployments that point at a local Ollama/vLLM gateway.
	SSRFStrict bool
	// DraftsFile, when non-empty, persists the draft box to that JSON file so
	// drafts survive a server restart. Empty keeps the stateless in-memory store.
	DraftsFile string
}

func Load() Config {
	return Config{
		Addr:           getEnv("APP_ADDR", ":8080"),
		AccessToken:    getEnv("APP_ACCESS_TOKEN", ""),
		GitHubClientID: getEnv("GITHUB_CLIENT_ID", ""),
		GitHubSecret:   getEnv("GITHUB_CLIENT_SECRET", ""),
		PublicBaseURL:  strings.TrimRight(strings.TrimSpace(getEnv("APP_PUBLIC_URL", "")), "/"),
		SSRFStrict:     isTruthy(getEnv("SSRF_STRICT", "")),
		DraftsFile:     strings.TrimSpace(getEnv("DRAFTS_FILE", "")),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// isTruthy treats 1/true/yes/on (case-insensitive) as enabled.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
