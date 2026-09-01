package config

import "os"

type Config struct {
	Addr              string
	RepoOwner         string
	RepoName          string
	RepoBranch        string
	ProviderStorePath string
	EncryptionKey     string
	AccessToken       string
}

func Load() Config {
	return Config{
		Addr:              getEnv("APP_ADDR", ":8080"),
		RepoOwner:         getEnv("GITHUB_REPO_OWNER", "wszpwu1"),
		RepoName:          getEnv("GITHUB_REPO_NAME", "ZPWU-CODE"),
		RepoBranch:        getEnv("GITHUB_REPO_BRANCH", "main"),
		ProviderStorePath: getEnv("PROVIDER_STORE_PATH", "data/providers.json"),
		EncryptionKey:     getEnv("APP_ENCRYPTION_KEY", ""),
		AccessToken:       getEnv("APP_ACCESS_TOKEN", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
