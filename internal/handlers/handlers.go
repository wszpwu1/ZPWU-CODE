package handlers

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wszpwu1/ZPWU-CODE/internal/agent"
	"github.com/wszpwu1/ZPWU-CODE/internal/config"
)

// ─── Request / Response types ───────────────────────────────────────────────

type jsonResponse map[string]any

// chatRequest — provider info travels with every request; server stores nothing.
type chatRequest struct {
	Message    string            `json:"message"`
	System     string            `json:"system"`
	Context    string            `json:"context"` // file content injected before user message
	Agent      string            `json:"agent"`
	ProviderID string            `json:"provider_id"` // informational only
	Provider   providerInlineReq `json:"provider"`    // full provider inline
}

// providerInlineReq is sent by the browser on every AI call; never persisted.
type providerInlineReq struct {
	Name    string            `json:"name"`
	BaseURL string            `json:"base_url"`
	Model   string            `json:"model"`
	APIKey  string            `json:"api_key"`
	Kind    string            `json:"kind"` // "openai" | "claude"
	Headers map[string]string `json:"headers"`
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

type gitDirEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Type    string `json:"type"` // "file" | "dir"
	SHA     string `json:"sha"`
	Size    int    `json:"size,omitempty"`
	HTMLURL string `json:"html_url"`
}

type gitFileReadResult struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA     string `json:"sha"`
	Size    int    `json:"size"`
	HTMLURL string `json:"html_url"`
}

// ─── Task store (in-memory only) ────────────────────────────────────────────

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

func newTaskStore() *taskStore {
	return &taskStore{items: make(map[string]*taskRecord)}
}

func (s *taskStore) create(taskType string, input any) *taskRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	id := fmt.Sprintf("task-%d-%d", time.Now().UTC().UnixMilli(), s.seq.Add(1))
	task := &taskRecord{ID: id, Type: taskType, Status: taskQueued, CreatedAt: now, UpdatedAt: now, Input: input}
	s.items[id] = task
	s.order = append([]string{id}, s.order...)
	const maxTasks = 100
	if len(s.order) > maxTasks {
		kept := make([]string, 0, len(s.order))
		for _, tid := range s.order {
			if len(kept) < maxTasks {
				kept = append(kept, tid)
				continue
			}
			if t, ok := s.items[tid]; ok && (t.Status == taskCompleted || t.Status == taskFailed) {
				delete(s.items, tid)
			} else {
				kept = append(kept, tid)
			}
		}
		s.order = kept
	}
	return cloneTask(task)
}

func (s *taskStore) setStatus(id string, st taskStatus, result any, te *taskError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.items[id]
	if !ok {
		return
	}
	task.Status = st
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if result != nil {
		task.Result = result
	}
	if te != nil {
		task.Error = te
	}
}

func (s *taskStore) running(id string) { s.setStatus(id, taskRunning, nil, nil) }
func (s *taskStore) complete(id string, result any) {
	s.setStatus(id, taskCompleted, result, nil)
}
func (s *taskStore) fail(id, code, message string) {
	s.setStatus(id, taskFailed, nil, &taskError{Code: code, Message: message})
}

func (s *taskStore) get(id string) (*taskRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.items[id]
	if !ok {
		return nil, false
	}
	return cloneTask(t), true
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
		if t, ok := s.items[id]; ok {
			out = append(out, taskToResponse(t))
		}
	}
	return out
}

func cloneTask(t *taskRecord) *taskRecord {
	cp := *t
	if t.Error != nil {
		e := *t.Error
		cp.Error = &e
	}
	return &cp
}

func taskToResponse(t *taskRecord) jsonResponse {
	r := jsonResponse{
		"id": t.ID, "type": t.Type, "status": t.Status,
		"created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
	}
	if t.Input != nil {
		r["input"] = t.Input
	}
	if t.Result != nil {
		r["result"] = t.Result
	}
	if t.Error != nil {
		r["error"] = t.Error
	}
	return r
}

// ─── LLM Gateway ────────────────────────────────────────────────────────────

type llmGateway struct {
	client *http.Client
}

func newLLMGateway() *llmGateway {
	return &llmGateway{client: &http.Client{Timeout: 90 * time.Second}}
}

// chat dispatches to OpenAI-compatible or Claude API depending on provider.Kind.
func (g *llmGateway) chat(ctx context.Context, req chatRequest) (string, map[string]any, error) {
	p := req.Provider
	if strings.TrimSpace(p.APIKey) == "" {
		return "", nil, errors.New("provider api_key is required")
	}
	if strings.TrimSpace(p.BaseURL) == "" && strings.TrimSpace(p.Kind) != "claude" {
		return "", nil, errors.New("provider base_url is required")
	}

	userContent := strings.TrimSpace(req.Message)
	if strings.TrimSpace(req.Context) != "" {
		userContent = "以下是相关文件内容供参考：\n\n```\n" + strings.TrimSpace(req.Context) + "\n```\n\n" + userContent
	}

	if strings.ToLower(strings.TrimSpace(p.Kind)) == "claude" {
		return g.chatClaude(ctx, p, strings.TrimSpace(req.System), userContent)
	}
	return g.chatOpenAI(ctx, p, strings.TrimSpace(req.System), userContent)
}

// ── OpenAI-compatible ────────────────────────────────────────────────────────

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage map[string]any `json:"usage"`
}

func (g *llmGateway) chatOpenAI(ctx context.Context, p providerInlineReq, system, userContent string) (string, map[string]any, error) {
	endpoint, err := normalizeChatEndpoint(p.BaseURL)
	if err != nil {
		return "", nil, err
	}
	messages := make([]jsonResponse, 0, 3)
	if system != "" {
		messages = append(messages, jsonResponse{"role": "system", "content": system})
	}
	messages = append(messages, jsonResponse{"role": "user", "content": userContent})

	payload := jsonResponse{"model": p.Model, "messages": messages, "stream": false}
	bodyBytes, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", nil, fmt.Errorf("build upstream request failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	for k, v := range p.Headers {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Content-Type") {
			continue
		}
		httpReq.Header.Set(k, v)
	}
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, safeSnippet(string(raw)))
	}
	var parsed openAIChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", nil, errors.New("parse upstream response failed")
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", nil, errors.New("upstream returned empty reply")
	}
	return parsed.Choices[0].Message.Content, parsed.Usage, nil
}

// ── Claude (Anthropic Messages API) ─────────────────────────────────────────

type claudeMessagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage map[string]any `json:"usage"`
}

func (g *llmGateway) chatClaude(ctx context.Context, p providerInlineReq, system, userContent string) (string, map[string]any, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	endpoint := baseURL + "/v1/messages"

	model := p.Model
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	payload := jsonResponse{
		"model":      model,
		"max_tokens": 8192,
		"messages":   []jsonResponse{{"role": "user", "content": userContent}},
	}
	if system != "" {
		payload["system"] = system
	}
	bodyBytes, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", nil, fmt.Errorf("build claude request failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range p.Headers {
		lk := strings.ToLower(k)
		if lk == "x-api-key" || lk == "content-type" || lk == "anthropic-version" {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("claude request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", nil, fmt.Errorf("claude returned %d: %s", resp.StatusCode, safeSnippet(string(raw)))
	}
	var parsed claudeMessagesResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", nil, errors.New("parse claude response failed")
	}
	for _, block := range parsed.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return block.Text, parsed.Usage, nil
		}
	}
	return "", nil, errors.New("claude returned empty reply")
}

// ─── GitHub helpers ──────────────────────────────────────────────────────────

func syncToGitHub(ctx context.Context, token string, req gitSyncRequest) (gitCommitResult, string, error) {
	safePath, err := sanitizeRepoPath(req.FilePath)
	if err != nil {
		return gitCommitResult{}, "invalid_file_path", err
	}
	client := &http.Client{Timeout: 45 * time.Second}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s",
		url.PathEscape(req.Owner), url.PathEscape(req.Repo), escapeGitHubContentPath(safePath))

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
		return gitCommitResult{}, "github_conflict", errors.New("branch conflict, refresh branch or choose another")
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
	return gitCommitResult{CommitSHA: parsed.Commit.SHA, HTMLURL: parsed.Commit.HTMLURL, FilePath: safePath, Branch: req.Branch}, "", nil
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

// normalizeChatEndpoint delegates to agent.NormalizeOpenAIEndpoint (#fix9: single implementation).
func normalizeChatEndpoint(base string) (string, error) {
	return agent.NormalizeOpenAIEndpoint(base)
}

// ─── Route Registration ──────────────────────────────────────────────────────

func RegisterRoutes(mux *http.ServeMux, cfg config.Config) {
	tasks := newTaskStore()
	gateway := newLLMGateway()
	ghClient := &http.Client{Timeout: 30 * time.Second}

	// ── Health ───────────────────────────────────────────────────────────────
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, jsonResponse{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
			"checks": jsonResponse{
				"github_oauth": cfg.GitHubClientID != "",
				"storage":      "github-only (stateless)",
			},
		})
	})

	// ── GitHub OAuth — step 1: redirect ──────────────────────────────────────
	// GET /api/auth/github
	mux.HandleFunc("/api/auth/github", func(w http.ResponseWriter, r *http.Request) {
		if cfg.GitHubClientID == "" {
			writeError(w, http.StatusServiceUnavailable, "oauth_not_configured", "GITHUB_CLIENT_ID not configured")
			return
		}
		redirectURI := strings.TrimRight(appBaseURL(r), "/") + "/api/auth/github/callback"
		authURL := fmt.Sprintf(
			"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=repo&state=zpwu",
			url.QueryEscape(cfg.GitHubClientID),
			url.QueryEscape(redirectURI),
		)
		http.Redirect(w, r, authURL, http.StatusFound)
	})

	// ── GitHub OAuth — step 2: callback ──────────────────────────────────────
	// GET /api/auth/github/callback
	mux.HandleFunc("/api/auth/github/callback", func(w http.ResponseWriter, r *http.Request) {
		if cfg.GitHubClientID == "" || cfg.GitHubSecret == "" {
			writeError(w, http.StatusServiceUnavailable, "oauth_not_configured", "OAuth not configured")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			writeError(w, http.StatusBadRequest, "missing_code", "missing OAuth code")
			return
		}
		// exchange code for token
		tokenBody, _ := json.Marshal(jsonResponse{
			"client_id":     cfg.GitHubClientID,
			"client_secret": cfg.GitHubSecret,
			"code":          code,
		})
		tokenReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			"https://github.com/login/oauth/access_token", bytes.NewReader(tokenBody))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "token_exchange_failed", "build token request failed")
			return
		}
		tokenReq.Header.Set("Accept", "application/json")
		tokenReq.Header.Set("Content-Type", "application/json")
		tokenResp, err := ghClient.Do(tokenReq)
		if err != nil {
			writeError(w, http.StatusBadGateway, "token_exchange_failed", "token exchange request failed")
			return
		}
		defer tokenResp.Body.Close()
		var tokenData struct {
			AccessToken string `json:"access_token"`
			Scope       string `json:"scope"`
			Error       string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(tokenResp.Body, 1<<20))
		if err := json.Unmarshal(raw, &tokenData); err != nil || tokenData.AccessToken == "" {
			msg := tokenData.Error
			if msg == "" {
				msg = "empty access token"
			}
			writeError(w, http.StatusBadGateway, "token_exchange_failed", msg)
			return
		}
		// fetch user info
		// URL is hardcoded so NewRequestWithContext will never fail; _ is intentional.
		userReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.github.com/user", nil)
		userReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
		userReq.Header.Set("Accept", "application/vnd.github+json")
		userReq.Header.Set("User-Agent", "ZPWU-CODE-agent")
		userResp, err := ghClient.Do(userReq)
		var login, avatarURL string
		if err == nil {
			defer userResp.Body.Close()
			var ud struct {
				Login     string `json:"login"`
				AvatarURL string `json:"avatar_url"`
			}
			rawU, _ := io.ReadAll(io.LimitReader(userResp.Body, 1<<20))
			_ = json.Unmarshal(rawU, &ud)
			login = ud.Login
			avatarURL = ud.AvatarURL
		}
		// Pass token back to browser via a tiny HTML page that stores it in localStorage
		// and redirects to root. This avoids token appearing in URL fragment leaks.
		escapedToken := template_jsString(tokenData.AccessToken)
		escapedLogin := template_jsString(login)
		escapedAvatar := template_jsString(avatarURL)
		html := fmt.Sprintf(`<!doctype html><html><head><meta charset="UTF-8">
<title>ZPWU — 登录中...</title></head><body>
<script>
try {
  var d = JSON.parse(localStorage.getItem('zpwu_config') || '{}');
  d.githubToken = %s;
  d.githubLogin = %s;
  d.githubAvatar = %s;
  localStorage.setItem('zpwu_config', JSON.stringify(d));
} catch(e) {}
window.location.replace('/');
</script>
<p>登录成功，正在跳转...</p>
</body></html>`, escapedToken, escapedLogin, escapedAvatar)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	})

	// ── GitHub user info (verify token) ──────────────────────────────────────
	// GET /api/auth/user  (X-GitHub-Token header)
	mux.HandleFunc("/api/auth/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		token := strings.TrimSpace(r.Header.Get("X-GitHub-Token"))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "X-GitHub-Token required")
			return
		}
		httpReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.github.com/user", nil)
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("Accept", "application/vnd.github+json")
		httpReq.Header.Set("User-Agent", "ZPWU-CODE-agent")
		resp, err := ghClient.Do(httpReq)
		if err != nil {
			writeError(w, http.StatusBadGateway, "github_request_failed", err.Error())
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode == http.StatusUnauthorized {
			writeError(w, http.StatusUnauthorized, "invalid_token", "GitHub token is invalid or expired")
			return
		}
		var ud struct {
			Login     string `json:"login"`
			AvatarURL string `json:"avatar_url"`
			Name      string `json:"name"`
		}
		_ = json.Unmarshal(raw, &ud)
		writeJSON(w, http.StatusOK, jsonResponse{
			"login":      ud.Login,
			"name":       ud.Name,
			"avatar_url": ud.AvatarURL,
		})
	})

	// ── List user repos ───────────────────────────────────────────────────────
	// GET /api/auth/repos
	mux.HandleFunc("/api/auth/repos", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		token := strings.TrimSpace(r.Header.Get("X-GitHub-Token"))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "X-GitHub-Token required")
			return
		}
		apiURL := "https://api.github.com/user/repos?sort=updated&per_page=50&type=all"
		httpReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL, nil)
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("Accept", "application/vnd.github+json")
		httpReq.Header.Set("User-Agent", "ZPWU-CODE-agent")
		resp, err := ghClient.Do(httpReq)
		if err != nil {
			writeError(w, http.StatusBadGateway, "github_request_failed", err.Error())
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			writeError(w, http.StatusBadGateway, "github_read_failed", safeSnippet(string(raw)))
			return
		}
		var repos []struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
		}
		if err := json.Unmarshal(raw, &repos); err != nil {
			writeError(w, http.StatusBadGateway, "parse_failed", "parse repos failed")
			return
		}
		out := make([]jsonResponse, 0, len(repos))
		for _, r := range repos {
			out = append(out, jsonResponse{
				"full_name":      r.FullName,
				"default_branch": r.DefaultBranch,
				"private":        r.Private,
			})
		}
		writeJSON(w, http.StatusOK, jsonResponse{"repos": out})
	})

	// ── Tasks ─────────────────────────────────────────────────────────────────
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, jsonResponse{"tasks": tasks.list(20)})
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

	// ── Chat ──────────────────────────────────────────────────────────────────
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
		if strings.TrimSpace(req.Message) == "" {
			writeError(w, http.StatusBadRequest, "invalid_message", "message is required")
			return
		}
		if strings.TrimSpace(req.Provider.APIKey) == "" {
			writeError(w, http.StatusBadRequest, "missing_provider", "provider.api_key is required")
			return
		}
		task := tasks.create("chat", jsonResponse{
			"agent":    fallback(req.Agent, "default"),
			"provider": req.Provider.Name,
			"model":    req.Provider.Model,
			"kind":     req.Provider.Kind,
		})
		writeJSON(w, http.StatusAccepted, jsonResponse{"task_id": task.ID, "status": task.Status})

		go func(taskID string, request chatRequest) {
			tasks.running(taskID)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			reply, usage, err := gateway.chat(ctx, request)
			if err != nil {
				tasks.fail(taskID, "upstream_chat_failed", err.Error())
				return
			}
			tasks.complete(taskID, jsonResponse{
				"reply": reply,
				"meta": jsonResponse{
					"provider": request.Provider.Name,
					"model":    request.Provider.Model,
					"kind":     request.Provider.Kind,
					"usage":    usage,
				},
			})
		}(task.ID, req)
	})

	// ── Git sync (write file to GitHub) ──────────────────────────────────────
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
			writeError(w, http.StatusUnauthorized, "missing_github_token", "X-GitHub-Token header is required")
			return
		}
		var req gitSyncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "invalid json payload")
			return
		}
		if req.Owner == "" || req.Repo == "" || req.FilePath == "" || req.CommitMessage == "" {
			writeError(w, http.StatusBadRequest, "invalid_sync_request", "owner, repo, file_path and commit_message are required")
			return
		}
		if req.Branch == "" {
			req.Branch = "main"
		}
		task := tasks.create("git_sync", jsonResponse{
			"repo": req.Owner + "/" + req.Repo, "branch": req.Branch, "path": req.FilePath,
		})
		writeJSON(w, http.StatusAccepted, jsonResponse{"task_id": task.ID, "status": task.Status})

		go func(taskID string, request gitSyncRequest, ghToken string) {
			tasks.running(taskID)
			result, code, err := syncToGitHub(context.Background(), ghToken, request)
			if err != nil {
				tasks.fail(taskID, code, err.Error())
				return
			}
			tasks.complete(taskID, result)
		}(task.ID, req, token)
	})

	// ── List directory on GitHub ──────────────────────────────────────────────
	// GET /api/git/files?owner=&repo=&branch=&path=
	mux.HandleFunc("/api/git/files", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		token := strings.TrimSpace(r.Header.Get("X-GitHub-Token"))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing_github_token", "X-GitHub-Token required")
			return
		}
		q := r.URL.Query()
		owner := strings.TrimSpace(q.Get("owner"))
		repo := strings.TrimSpace(q.Get("repo"))
		branch := fallback(strings.TrimSpace(q.Get("branch")), "main")
		dirPath := strings.TrimSpace(strings.TrimPrefix(q.Get("path"), "/"))
		if owner == "" || repo == "" {
			writeError(w, http.StatusBadRequest, "missing_params", "owner and repo are required")
			return
		}
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
			url.PathEscape(owner), url.PathEscape(repo),
			escapeGitHubContentPath(dirPath), url.QueryEscape(branch))

		httpReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL, nil)
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("Accept", "application/vnd.github+json")
		httpReq.Header.Set("User-Agent", "ZPWU-CODE-agent")
		resp, err := ghClient.Do(httpReq)
		if err != nil {
			writeError(w, http.StatusBadGateway, "github_request_failed", err.Error())
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode == http.StatusNotFound {
			writeError(w, http.StatusNotFound, "path_not_found", "path not found in repository")
			return
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			writeError(w, http.StatusBadGateway, "github_read_failed", safeSnippet(string(raw)))
			return
		}
		var entries []gitDirEntry
		if err := json.Unmarshal(raw, &entries); err != nil {
			var single gitDirEntry
			if err2 := json.Unmarshal(raw, &single); err2 != nil {
				writeError(w, http.StatusBadGateway, "parse_failed", "parse github response failed")
				return
			}
			entries = []gitDirEntry{single}
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Type != entries[j].Type {
				return entries[i].Type == "dir"
			}
			return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
		})
		writeJSON(w, http.StatusOK, jsonResponse{
			"path": dirPath, "owner": owner, "repo": repo, "branch": branch, "entries": entries,
		})
	})

	// ── Read file from GitHub ─────────────────────────────────────────────────
	// GET /api/git/file?owner=&repo=&branch=&path=
	mux.HandleFunc("/api/git/file", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		token := strings.TrimSpace(r.Header.Get("X-GitHub-Token"))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing_github_token", "X-GitHub-Token required")
			return
		}
		q := r.URL.Query()
		owner := strings.TrimSpace(q.Get("owner"))
		repo := strings.TrimSpace(q.Get("repo"))
		branch := fallback(strings.TrimSpace(q.Get("branch")), "main")
		filePath := strings.TrimSpace(strings.TrimPrefix(q.Get("path"), "/"))
		if owner == "" || repo == "" || filePath == "" {
			writeError(w, http.StatusBadRequest, "missing_params", "owner, repo and path are required")
			return
		}
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
			url.PathEscape(owner), url.PathEscape(repo),
			escapeGitHubContentPath(filePath), url.QueryEscape(branch))

		httpReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL, nil)
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("Accept", "application/vnd.github+json")
		httpReq.Header.Set("User-Agent", "ZPWU-CODE-agent")
		resp, err := ghClient.Do(httpReq)
		if err != nil {
			writeError(w, http.StatusBadGateway, "github_request_failed", err.Error())
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode == http.StatusNotFound {
			writeError(w, http.StatusNotFound, "file_not_found", "file not found in repository")
			return
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			writeError(w, http.StatusBadGateway, "github_read_failed", safeSnippet(string(raw)))
			return
		}
		var ghFile struct {
			Name    string `json:"name"`
			Path    string `json:"path"`
			SHA     string `json:"sha"`
			Size    int    `json:"size"`
			HTMLURL string `json:"html_url"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &ghFile); err != nil {
			writeError(w, http.StatusBadGateway, "parse_failed", "parse github response failed")
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(ghFile.Content, "\n", ""))
		if err != nil {
			writeError(w, http.StatusBadGateway, "decode_failed", "base64 decode failed")
			return
		}
		writeJSON(w, http.StatusOK, jsonResponse{
			"file": gitFileReadResult{
				Name: ghFile.Name, Path: ghFile.Path,
				Content: string(decoded), SHA: ghFile.SHA,
				Size: ghFile.Size, HTMLURL: ghFile.HTMLURL,
			},
		})
	})

	// ── Agent run (SSE) ───────────────────────────────────────────────────────
	// POST /api/agent/run  →  text/event-stream
	mux.HandleFunc("/api/agent/run", func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRequest(w, r, cfg.AccessToken) {
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		ghToken := strings.TrimSpace(r.Header.Get("X-GitHub-Token"))
		if ghToken == "" {
			writeError(w, http.StatusUnauthorized, "missing_github_token", "X-GitHub-Token header is required")
			return
		}
		var req agent.AgentHTTPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "invalid json payload")
			return
		}
		if strings.TrimSpace(req.Message) == "" {
			writeError(w, http.StatusBadRequest, "missing_message", "message is required")
			return
		}
		if strings.TrimSpace(req.Provider.APIKey) == "" {
			writeError(w, http.StatusBadRequest, "missing_api_key", "provider.api_key is required")
			return
		}
		// #fix6: enforce minimum token length to prevent trivially-forged tokens
		// when APP_ACCESS_TOKEN is not set (open deployment)
		if len(ghToken) < 10 {
			writeError(w, http.StatusUnauthorized, "invalid_github_token", "X-GitHub-Token appears invalid (too short)")
			return
		}
		if req.Owner == "" || req.Repo == "" {
			writeError(w, http.StatusBadRequest, "missing_repo", "owner and repo are required")
			return
		}
		emitter, ok := agent.NewSSEEmitter(w)
		if !ok {
			writeError(w, http.StatusInternalServerError, "sse_not_supported", "SSE not supported by this transport")
			return
		}
		runReq := req.BuildRunRequest(ghToken)
		_, _ = agent.Run(r.Context(), runReq, emitter)
	})

	log.Println("routes registered (stateless mode — no disk storage)")
}

// ─── Utility ─────────────────────────────────────────────────────────────────

func authorizeRequest(w http.ResponseWriter, r *http.Request, expectedToken string) bool {
	expectedToken = strings.TrimSpace(expectedToken)
	if expectedToken == "" {
		return true // no server-level token required; GitHub token protects data
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
	writeJSON(w, status, jsonResponse{"error": jsonResponse{"code": code, "message": message}})
}

func fallback(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func safeSnippet(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 240 {
		text = text[:240] + "..."
	}
	return text
}

func appBaseURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + r.Host
}

// template_jsString safely encodes a Go string for embedding inside a JS string literal (single-quoted).
func template_jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b) // json.Marshal produces a valid JS string literal with double quotes
}
