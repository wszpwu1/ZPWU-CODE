package handlers

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wszpwu1/ZPWU-CODE/internal/config"
)

type chatRequest struct {
	Message    string `json:"message"`
	Agent      string `json:"agent"`
	ProviderID string `json:"provider_id"`
}

type jsonResponse map[string]any

type providerUpsertRequest struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	BaseURL string            `json:"base_url"`
	Model   string            `json:"model"`
	APIKey  string            `json:"api_key"`
	Headers map[string]string `json:"headers"`
	Active  bool              `json:"active"`
}

type setActiveProviderRequest struct {
	ID string `json:"id"`
}

type providerPublic struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	BaseURL    string            `json:"base_url"`
	Model      string            `json:"model"`
	Headers    map[string]string `json:"headers"`
	MaskedKey  string            `json:"masked_key"`
	Active     bool              `json:"active"`
	CreatedAt  string            `json:"created_at"`
	UpdatedAt  string            `json:"updated_at"`
	LastUsedAt string            `json:"last_used_at,omitempty"`
}

type providerRecord struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	BaseURL         string            `json:"base_url"`
	Model           string            `json:"model"`
	Headers         map[string]string `json:"headers"`
	EncryptedAPIKey string            `json:"encrypted_api_key"`
	MaskedKey       string            `json:"masked_key"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
	LastUsedAt      string            `json:"last_used_at,omitempty"`
}

type providerStorageFile struct {
	ActiveID  string           `json:"active_id"`
	Providers []providerRecord `json:"providers"`
}

type providerForUse struct {
	ID      string
	Name    string
	BaseURL string
	Model   string
	APIKey  string
	Headers map[string]string
}

type providerStore struct {
	mu       sync.RWMutex
	path     string
	key      []byte
	activeID string
	items    map[string]providerRecord
}

type taskStatus string

const (
	taskQueued    taskStatus = "queued"
	taskRunning   taskStatus = "running"
	taskCompleted taskStatus = "completed"
	taskFailed    taskStatus = "failed"
)

type taskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type taskRecord struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Status    taskStatus `json:"status"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
	Input     any        `json:"input,omitempty"`
	Result    any        `json:"result,omitempty"`
	Error     *taskError `json:"error,omitempty"`
}

type taskStore struct {
	mu    sync.RWMutex
	items map[string]*taskRecord
	order []string
	seq   atomic.Uint64
}

type gitSyncRequest struct {
	Owner         string `json:"owner"`
	Repo          string `json:"repo"`
	Branch        string `json:"branch"`
	FilePath      string `json:"file_path"`
	Content       string `json:"content"`
	CommitMessage string `json:"commit_message"`
}

type gitCommitResult struct {
	CommitSHA string `json:"commit_sha"`
	HTMLURL   string `json:"html_url"`
	FilePath  string `json:"file_path"`
	Branch    string `json:"branch"`
}

type commandValidationRequest struct {
	Commands []string `json:"commands"`
	Paths    []string `json:"paths"`
}

type commandValidationResult struct {
	Allowed    bool     `json:"allowed"`
	Violations []string `json:"violations"`
	Warnings   []string `json:"warnings"`
}

func RegisterRoutes(mux *http.ServeMux, cfg config.Config) {
	providers, err := newProviderStore(cfg.ProviderStorePath, cfg.EncryptionKey)
	if err != nil {
		log.Printf("provider store init error: %v", err)
	}
	tasks := newTaskStore()
	gateway := &llmGateway{
		client: &http.Client{Timeout: 45 * time.Second},
	}

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		providerConfigured := false
		if providers != nil {
			providerConfigured = providers.hasAny()
		}
		writeJSON(w, http.StatusOK, jsonResponse{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
			"checks": jsonResponse{
				"provider_configured": providerConfigured,
				"github_repo":         cfg.RepoOwner + "/" + cfg.RepoName + "@" + cfg.RepoBranch,
				"sandbox_mode":        "policy-validation-only",
			},
		})
	})

	mux.HandleFunc("/api/providers", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if providers == nil {
			writeError(w, http.StatusServiceUnavailable, "provider_store_unavailable", "provider store is unavailable")
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, jsonResponse{
				"providers": providers.listPublic(),
				"active_id": providers.activeProviderID(),
			})
		case http.MethodPost:
			var req providerUpsertRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", "invalid json payload")
				return
			}
			pub, err := providers.upsert(req)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_provider_config", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, jsonResponse{
				"provider": pub,
			})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
	})

	mux.HandleFunc("/api/providers/active", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if providers == nil {
			writeError(w, http.StatusServiceUnavailable, "provider_store_unavailable", "provider store is unavailable")
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req setActiveProviderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "invalid json payload")
			return
		}
		if err := providers.setActive(req.ID); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_provider_id", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, jsonResponse{
			"active_id": req.ID,
		})
	})

	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, jsonResponse{
			"tasks": tasks.list(20),
		})
	})

	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		if id == "" {
			writeError(w, http.StatusBadRequest, "invalid_task_id", "task id is required")
			return
		}
		task, ok := tasks.get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "task_not_found", "task not found")
			return
		}
		writeJSON(w, http.StatusOK, taskToResponse(task))
	})

	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}

		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "invalid json payload")
			return
		}
		if req.Message == "" {
			writeError(w, http.StatusBadRequest, "invalid_message", "message is required")
			return
		}
		task := tasks.create("chat", jsonResponse{
			"agent":       fallback(req.Agent, "default"),
			"provider_id": req.ProviderID,
			"message_len": len(req.Message),
		})
		writeJSON(w, http.StatusAccepted, jsonResponse{
			"task_id": task.ID,
			"status":  task.Status,
		})

		if providers == nil {
			tasks.fail(task.ID, "provider_store_unavailable", "provider store is unavailable")
			return
		}
		go func(taskID string, request chatRequest) {
			tasks.running(taskID)
			provider, err := providers.resolveProvider(request.ProviderID)
			if err != nil {
				tasks.fail(taskID, "provider_not_available", err.Error())
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			reply, usage, err := gateway.chat(ctx, provider, request.Message)
			if err != nil {
				tasks.fail(taskID, "upstream_chat_failed", err.Error())
				return
			}
			providers.markUsed(provider.ID)
			tasks.complete(taskID, jsonResponse{
				"reply": reply,
				"meta": jsonResponse{
					"agent":       fallback(request.Agent, "default"),
					"provider":    provider.Name,
					"provider_id": provider.ID,
					"model":       provider.Model,
					"usage":       usage,
				},
			})
		}(task.ID, req)
	})

	mux.HandleFunc("/api/exec/validate", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req commandValidationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "invalid json payload")
			return
		}
		res := validateCommands(req.Commands, req.Paths)
		writeJSON(w, http.StatusOK, jsonResponse{
			"result": res,
		})
	})

	mux.HandleFunc("/api/git/sync", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		token := strings.TrimSpace(r.Header.Get("X-GitHub-Token"))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing_github_token", "github token is required in X-GitHub-Token header")
			return
		}
		var req gitSyncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "invalid json payload")
			return
		}
		req.Owner = fallback(req.Owner, cfg.RepoOwner)
		req.Repo = fallback(req.Repo, cfg.RepoName)
		req.Branch = fallback(req.Branch, cfg.RepoBranch)
		if req.FilePath == "" || req.CommitMessage == "" {
			writeError(w, http.StatusBadRequest, "invalid_sync_request", "file_path and commit_message are required")
			return
		}

		task := tasks.create("git_sync", jsonResponse{
			"repo":   req.Owner + "/" + req.Repo,
			"branch": req.Branch,
			"path":   req.FilePath,
		})
		writeJSON(w, http.StatusAccepted, jsonResponse{
			"task_id": task.ID,
			"status":  task.Status,
		})

		go func(taskID string, request gitSyncRequest, githubToken string) {
			tasks.running(taskID)
			result, code, err := syncToGitHub(context.Background(), githubToken, request)
			if err != nil {
				tasks.fail(taskID, code, err.Error())
				return
			}
			tasks.complete(taskID, result)
		}(task.ID, req, token)
	})
}

type llmGateway struct {
	client *http.Client
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage map[string]any `json:"usage"`
}

func (g *llmGateway) chat(ctx context.Context, provider providerForUse, message string) (string, map[string]any, error) {
	endpoint, err := normalizeChatEndpoint(provider.BaseURL)
	if err != nil {
		return "", nil, err
	}
	payload := jsonResponse{
		"model": provider.Model,
		"messages": []jsonResponse{
			{
				"role":    "user",
				"content": message,
			},
		},
		"stream": false,
	}
	bodyBytes, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", nil, fmt.Errorf("build upstream request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	for k, v := range provider.Headers {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Content-Type") {
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, safeSnippet(string(raw)))
	}
	var parsed openAIChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", nil, fmt.Errorf("parse upstream response failed")
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", nil, errors.New("upstream returned empty reply")
	}
	return parsed.Choices[0].Message.Content, parsed.Usage, nil
}

func normalizeChatEndpoint(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", errors.New("base_url is required")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("base_url must be a valid absolute URL")
	}
	if strings.HasSuffix(strings.TrimRight(base, "/"), "/v1/chat/completions") {
		return strings.TrimRight(base, "/"), nil
	}
	return strings.TrimRight(base, "/") + "/v1/chat/completions", nil
}

func syncToGitHub(ctx context.Context, token string, req gitSyncRequest) (gitCommitResult, string, error) {
	safePath, err := sanitizeRepoPath(req.FilePath)
	if err != nil {
		return gitCommitResult{}, "invalid_file_path", err
	}

	client := &http.Client{Timeout: 45 * time.Second}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", req.Owner, req.Repo, escapeGitHubContentPath(safePath))
	existingSHA, readCode, readErr := readContentSHA(ctx, client, token, baseURL, req.Branch)
	if readErr != nil && readCode != "github_content_not_found" {
		return gitCommitResult{}, readCode, readErr
	}

	payload := jsonResponse{
		"message": req.CommitMessage,
		"content": base64.StdEncoding.EncodeToString([]byte(req.Content)),
		"branch":  req.Branch,
	}
	if existingSHA != "" {
		payload["sha"] = existingSHA
	}
	bodyBytes, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return gitCommitResult{}, "github_request_failed", fmt.Errorf("build github request failed")
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("User-Agent", "ZPWU-CODE-agent")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return gitCommitResult{}, "github_request_failed", fmt.Errorf("github request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode == http.StatusConflict {
		return gitCommitResult{}, "github_conflict", errors.New("github branch conflict, please refresh branch or choose another branch")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return gitCommitResult{}, "github_write_failed", fmt.Errorf("github returned %d: %s", resp.StatusCode, safeSnippet(string(raw)))
	}

	var parsed struct {
		Commit struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
		} `json:"commit"`
	}
	_ = json.Unmarshal(raw, &parsed)
	return gitCommitResult{
		CommitSHA: parsed.Commit.SHA,
		HTMLURL:   parsed.Commit.HTMLURL,
		FilePath:  safePath,
		Branch:    req.Branch,
	}, "", nil
}

func readContentSHA(ctx context.Context, client *http.Client, token, contentURL, branch string) (string, string, error) {
	getURL := contentURL + "?ref=" + url.QueryEscape(branch)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return "", "github_request_failed", errors.New("build github request failed")
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("User-Agent", "ZPWU-CODE-agent")
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", "github_request_failed", fmt.Errorf("github request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return "", "github_content_not_found", errors.New("content not found")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", "github_read_failed", fmt.Errorf("github returned %d: %s", resp.StatusCode, safeSnippet(string(raw)))
	}
	var parsed struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "github_read_failed", errors.New("parse github response failed")
	}
	return parsed.SHA, "", nil
}

func sanitizeRepoPath(input string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(input, "/"))
	if trimmed == "" {
		return "", errors.New("file_path is required")
	}
	if strings.Contains(trimmed, "..") || strings.HasPrefix(trimmed, ".git") || strings.Contains(trimmed, "/.git") {
		return "", errors.New("file_path contains restricted location")
	}
	cleaned := filepath.ToSlash(filepath.Clean(trimmed))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", errors.New("file_path is invalid")
	}
	return cleaned, nil
}

func escapeGitHubContentPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func validateCommands(commands, paths []string) commandValidationResult {
	allowedPrefixes := []string{
		"go test", "go run", "go fmt", "git status", "git diff", "git add", "git commit", "npm run", "npm test", "ls", "cat",
	}
	forbiddenTokens := []string{"sudo ", "rm -rf /", "mkfs", ":(){:|:&};:", "curl ", "wget ", "chmod 777", "chown "}
	var violations []string
	var warnings []string

	for _, cmd := range commands {
		cmd = strings.TrimSpace(strings.ToLower(cmd))
		if cmd == "" {
			continue
		}
		matched := false
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(cmd, strings.ToLower(prefix)) {
				matched = true
				break
			}
		}
		if !matched {
			violations = append(violations, "command not in allow-list: "+cmd)
		}
		for _, token := range forbiddenTokens {
			if strings.Contains(cmd, token) {
				violations = append(violations, "dangerous token detected: "+token)
			}
		}
		if strings.Contains(cmd, "| sh") || strings.Contains(cmd, "| bash") {
			violations = append(violations, "piped shell execution is blocked")
		}
	}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, "..") || strings.HasPrefix(p, "/etc") || strings.HasPrefix(p, "/root") || strings.Contains(p, "/.git") {
			violations = append(violations, "sensitive path blocked: "+p)
			continue
		}
		if strings.HasPrefix(p, "/tmp") {
			warnings = append(warnings, "temporary path used: "+p)
		}
	}
	return commandValidationResult{
		Allowed:    len(violations) == 0,
		Violations: dedup(violations),
		Warnings:   dedup(warnings),
	}
}

func dedup(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func newProviderStore(path, encryptionKey string) (*providerStore, error) {
	if len(strings.TrimSpace(encryptionKey)) < 16 {
		return nil, errors.New("APP_ENCRYPTION_KEY must be set and at least 16 characters")
	}
	sum := sha256.Sum256([]byte(encryptionKey))
	store := &providerStore{
		path:  path,
		key:   sum[:],
		items: make(map[string]providerRecord),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *providerStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var saved providerStorageFile
	if err := json.Unmarshal(b, &saved); err != nil {
		return err
	}
	s.activeID = saved.ActiveID
	for _, item := range saved.Providers {
		s.items[item.ID] = item
	}
	return nil
}

func (s *providerStore) persist() error {
	saved := providerStorageFile{
		ActiveID: s.activeID,
	}
	for _, item := range s.items {
		saved.Providers = append(saved.Providers, item)
	}
	sort.Slice(saved.Providers, func(i, j int) bool {
		return saved.Providers[i].UpdatedAt > saved.Providers[j].UpdatedAt
	})
	body, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *providerStore) upsert(req providerUpsertRequest) (providerPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.Model = strings.TrimSpace(req.Model)
	if req.Name == "" || req.BaseURL == "" || req.Model == "" {
		return providerPublic{}, errors.New("name, base_url, and model are required")
	}
	if _, err := normalizeChatEndpoint(req.BaseURL); err != nil {
		return providerPublic{}, err
	}
	if req.ID == "" {
		req.ID = fmt.Sprintf("provider-%d", time.Now().UTC().UnixMilli())
	}
	old, exists := s.items[req.ID]
	now := time.Now().UTC().Format(time.RFC3339)
	encKey := old.EncryptedAPIKey
	if strings.TrimSpace(req.APIKey) != "" {
		encrypted, err := encryptString(strings.TrimSpace(req.APIKey), s.key)
		if err != nil {
			return providerPublic{}, errors.New("encrypt api key failed")
		}
		encKey = encrypted
	}
	if encKey == "" {
		return providerPublic{}, errors.New("api_key is required for new provider")
	}
	record := providerRecord{
		ID:              req.ID,
		Name:            req.Name,
		BaseURL:         strings.TrimRight(req.BaseURL, "/"),
		Model:           req.Model,
		Headers:         cloneMap(req.Headers),
		EncryptedAPIKey: encKey,
		MaskedKey:       old.MaskedKey,
		CreatedAt:       old.CreatedAt,
		UpdatedAt:       now,
		LastUsedAt:      old.LastUsedAt,
	}
	if strings.TrimSpace(req.APIKey) != "" {
		record.MaskedKey = maskSecret(strings.TrimSpace(req.APIKey))
	}
	if !exists || record.CreatedAt == "" {
		record.CreatedAt = now
	}
	s.items[record.ID] = record
	if req.Active || s.activeID == "" {
		s.activeID = record.ID
	}
	if err := s.persist(); err != nil {
		return providerPublic{}, err
	}
	return s.toPublic(record), nil
}

func (s *providerStore) setActive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return errors.New("provider not found")
	}
	s.activeID = id
	return s.persist()
}

func (s *providerStore) markUsed(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return
	}
	item.LastUsedAt = time.Now().UTC().Format(time.RFC3339)
	item.UpdatedAt = item.LastUsedAt
	s.items[id] = item
	_ = s.persist()
}

func (s *providerStore) resolveProvider(providerID string) (providerForUse, error) {
	s.mu.RLock()
	id := strings.TrimSpace(providerID)
	if id == "" {
		id = s.activeID
	}
	item, ok := s.items[id]
	key := s.key
	s.mu.RUnlock()
	if !ok {
		return providerForUse{}, errors.New("active provider is not configured")
	}
	apiKey, err := decryptString(item.EncryptedAPIKey, key)
	if err != nil {
		return providerForUse{}, errors.New("provider api key decrypt failed")
	}
	return providerForUse{
		ID:      item.ID,
		Name:    item.Name,
		BaseURL: item.BaseURL,
		Model:   item.Model,
		APIKey:  apiKey,
		Headers: cloneMap(item.Headers),
	}, nil
}

func (s *providerStore) listPublic() []providerPublic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]providerPublic, 0, len(s.items))
	for _, item := range s.items {
		pub := s.toPublic(item)
		result = append(result, pub)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].UpdatedAt > result[j].UpdatedAt
	})
	return result
}

func (s *providerStore) toPublic(item providerRecord) providerPublic {
	return providerPublic{
		ID:         item.ID,
		Name:       item.Name,
		BaseURL:    item.BaseURL,
		Model:      item.Model,
		Headers:    cloneMap(item.Headers),
		MaskedKey:  item.MaskedKey,
		Active:     item.ID == s.activeID,
		CreatedAt:  item.CreatedAt,
		UpdatedAt:  item.UpdatedAt,
		LastUsedAt: item.LastUsedAt,
	}
}

func (s *providerStore) hasAny() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items) > 0
}

func (s *providerStore) activeProviderID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeID
}

func cloneMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func encryptString(plain string, key []byte) (string, error) {
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, []byte(plain), nil)
	all := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(all), nil
}

func decryptString(encrypted string, key []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	size := aead.NonceSize()
	if len(raw) <= size {
		return "", errors.New("encrypted payload is invalid")
	}
	nonce, ciphertext := raw[:size], raw[size:]
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func maskSecret(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) < 10 {
		return "****"
	}
	return raw[:4] + "..." + raw[len(raw)-4:]
}

func newTaskStore() *taskStore {
	return &taskStore{
		items: make(map[string]*taskRecord),
	}
}

func (s *taskStore) create(taskType string, input any) *taskRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	id := fmt.Sprintf("task-%d-%d", time.Now().UTC().UnixMilli(), s.seq.Add(1))
	task := &taskRecord{
		ID:        id,
		Type:      taskType,
		Status:    taskQueued,
		CreatedAt: now,
		UpdatedAt: now,
		Input:     input,
	}
	s.items[id] = task
	s.order = append([]string{id}, s.order...)
	const maxTrackedTasks = 100
	if len(s.order) > maxTrackedTasks {
		kept := make([]string, 0, len(s.order))
		for _, taskID := range s.order {
			if len(kept) < maxTrackedTasks {
				kept = append(kept, taskID)
				continue
			}
			staleTask, ok := s.items[taskID]
			if !ok {
				continue
			}
			if staleTask.Status == taskCompleted || staleTask.Status == taskFailed {
				delete(s.items, taskID)
				continue
			}
			kept = append(kept, taskID)
		}
		s.order = kept
	}
	return cloneTask(task)
}

func (s *taskStore) running(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.items[id]
	if !ok {
		return
	}
	task.Status = taskRunning
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

func (s *taskStore) complete(id string, result any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.items[id]
	if !ok {
		return
	}
	task.Status = taskCompleted
	task.Result = result
	task.Error = nil
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

func (s *taskStore) fail(id, code, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.items[id]
	if !ok {
		return
	}
	task.Status = taskFailed
	task.Error = &taskError{
		Code:    code,
		Message: message,
	}
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

func (s *taskStore) get(id string) (*taskRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.items[id]
	if !ok {
		return nil, false
	}
	return cloneTask(task), true
}

func (s *taskStore) list(limit int) []jsonResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 20
	}
	out := make([]jsonResponse, 0, limit)
	for _, id := range s.order {
		if len(out) >= limit {
			break
		}
		task, ok := s.items[id]
		if !ok {
			continue
		}
		out = append(out, taskToResponse(task))
	}
	return out
}

func cloneTask(task *taskRecord) *taskRecord {
	copyTask := *task
	if task.Error != nil {
		errCopy := *task.Error
		copyTask.Error = &errCopy
	}
	return &copyTask
}

func taskToResponse(task *taskRecord) jsonResponse {
	res := jsonResponse{
		"id":         task.ID,
		"type":       task.Type,
		"status":     task.Status,
		"created_at": task.CreatedAt,
		"updated_at": task.UpdatedAt,
	}
	if task.Input != nil {
		res["input"] = task.Input
	}
	if task.Result != nil {
		res["result"] = task.Result
	}
	if task.Error != nil {
		res["error"] = task.Error
	}
	return res
}

func safeSnippet(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 240 {
		text = text[:240] + "..."
	}
	return text
}

func authorizeRequest(w http.ResponseWriter, r *http.Request, expectedToken string) bool {
	expectedToken = strings.TrimSpace(expectedToken)
	if expectedToken == "" {
		writeError(w, http.StatusServiceUnavailable, "access_token_not_configured", "APP_ACCESS_TOKEN is required for protected endpoints")
		return false
	}
	actual := strings.TrimSpace(r.Header.Get("X-App-Token"))
	if subtle.ConstantTimeCompare([]byte(actual), []byte(expectedToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid X-App-Token")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, data jsonResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, jsonResponse{
		"error": jsonResponse{
			"code":    code,
			"message": message,
		},
	})
}

func fallback(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
