package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/wszpwu1/ZPWU-CODE/internal/config"
)

type chatRequest struct {
	Message string `json:"message"`
	Agent   string `json:"agent"`
}

type jsonResponse map[string]any

func RegisterRoutes(mux *http.ServeMux, cfg config.Config) {
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, jsonResponse{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, jsonResponse{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, jsonResponse{"error": "method not allowed"})
			return
		}

		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, jsonResponse{"error": "invalid json payload"})
			return
		}
		if req.Message == "" {
			writeJSON(w, http.StatusBadRequest, jsonResponse{"error": "message is required"})
			return
		}
		// Placeholder only: non-empty key check for scaffold stage.
		if r.Header.Get("X-API-Key") == "" {
			w.Header().Set("WWW-Authenticate", `APIKey realm="chat", error="missing_key"`)
			writeJSON(w, http.StatusUnauthorized, jsonResponse{"error": "api key is required"})
			return
		}

		writeJSON(w, http.StatusOK, jsonResponse{
			"reply": "[skeleton] backend received message: " + req.Message,
			"meta": jsonResponse{
				"agent": fallback(req.Agent, "default"),
				"repo":  cfg.RepoOwner + "/" + cfg.RepoName + "@" + cfg.RepoBranch,
			},
		})
	})

	mux.HandleFunc("/api/git/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, jsonResponse{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusAccepted, jsonResponse{
			"status":  "queued",
			"message": "git sync placeholder, implement with git pull or GitHub API later",
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, data jsonResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func fallback(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
