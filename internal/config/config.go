package config

import "os"

type Config struct {
	Addr           string
	AccessToken    string // server-level guard token (optional, for self-hosted protection)
	GitHubClientID string
	GitHubSecret   string
}

func Load() Config {
	return Config{
		Addr:           getEnv("APP_ADDR", ":8080"),
		AccessToken:    getEnv("APP_ACCESS_TOKEN", ""),
		GitHubClientID: getEnv("GITHUB_CLIENT_ID", ""),
		GitHubSecret:   getEnv("GITHUB_CLIENT_SECRET", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
