// Package agent implements the tool definitions and execution layer for the
// ZPWU agent loop. Tools operate exclusively against the GitHub Contents API
// so the server remains fully stateless.
package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// ─── Tool definitions (sent to LLM) ─────────────────────────────────────────

// ToolDef describes a tool for the LLM (OpenAI function-calling schema).
type ToolDef struct {
	Type     string       `json:"type"` // always "function"
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  ToolParameters `json:"parameters"`
}

type ToolParameters struct {
	Type       string             `json:"type"` // "object"
	Properties map[string]PropDef `json:"properties"`
	Required   []string           `json:"required"`
}

type PropDef struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// AllTools is the tool list sent to every LLM call.
var AllTools = []ToolDef{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "list_dir",
			Description: "List files and directories in a GitHub repository path. Use this to explore the repository structure before reading or writing files. Always use this first when you need to understand the project layout.",
			Parameters: ToolParameters{
				Type: "object",
				Properties: map[string]PropDef{
					"path": {Type: "string", Description: "Directory path relative to repository root. Use empty string or '.' for root."},
				},
				Required: []string{},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "read_file",
			Description: "Read the contents of a file from the GitHub repository. Returns the full UTF-8 content of the file.",
			Parameters: ToolParameters{
				Type: "object",
				Properties: map[string]PropDef{
					"path": {Type: "string", Description: "File path relative to repository root (e.g. 'src/main.go')."},
				},
				Required: []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "write_file",
			Description: "Write or update a file in the GitHub repository with a commit. Use this to create new files or save changes.",
			Parameters: ToolParameters{
				Type: "object",
				Properties: map[string]PropDef{
					"path":           {Type: "string", Description: "File path relative to repository root."},
					"content":        {Type: "string", Description: "Full file content to write."},
					"commit_message": {Type: "string", Description: "Git commit message describing the change."},
				},
				Required: []string{"path", "content", "commit_message"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name: "search_files",
			// #fix8: document GitHub search limitation and fallback strategy
			Description: "Search for files in the repository by name or extension using GitHub code search API. Returns matching file paths. NOTE: GitHub search index may not be available for newly created or very small repositories — if you get no results or a search error, use list_dir instead to browse the directory tree.",
			Parameters: ToolParameters{
				Type: "object",
				Properties: map[string]PropDef{
					"query": {Type: "string", Description: "Search query, e.g. 'filename:*.go' or 'extension:js' or a specific filename like 'main.go'."},
				},
				Required: []string{"query"},
			},
		},
	},
}

// ─── Tool call input from LLM ────────────────────────────────────────────────

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

// ─── GitHub tool executor ────────────────────────────────────────────────────

// RepoCtx holds the repository context for tool execution.
type RepoCtx struct {
	Owner  string
	Repo   string
	Branch string
	Token  string
}

// Executor executes tool calls against GitHub.
type Executor struct {
	client *http.Client
	repo   RepoCtx
}

// NewExecutor creates a new tool executor.
func NewExecutor(repo RepoCtx) *Executor {
	return &Executor{
		client: &http.Client{Timeout: 30 * time.Second},
		repo:   repo,
	}
}

// Execute dispatches a tool call and returns the result string.
func (e *Executor) Execute(ctx context.Context, tc ToolCall) (string, error) {
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return "", fmt.Errorf("invalid tool arguments: %w", err)
	}
	switch tc.Function.Name {
	case "list_dir":
		return e.listDir(ctx, args["path"])
	case "read_file":
		return e.readFile(ctx, args["path"])
	case "write_file":
		return e.writeFile(ctx, args["path"], args["content"], args["commit_message"])
	case "search_files":
		return e.searchFiles(ctx, args["query"])
	default:
		return "", fmt.Errorf("unknown tool: %s", tc.Function.Name)
	}
}

// ── list_dir ─────────────────────────────────────────────────────────────────

func (e *Executor) listDir(ctx context.Context, dirPath string) (string, error) {
	dirPath = sanitizePath(dirPath)
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
		url.PathEscape(e.repo.Owner), url.PathEscape(e.repo.Repo),
		escapeContentPath(dirPath), url.QueryEscape(e.repo.Branch))

	raw, status, err := e.ghGET(ctx, apiURL)
	if err != nil {
		return "", err
	}
	if status == 404 {
		return fmt.Sprintf("path '%s' not found in repository", dirPath), nil
	}
	if status < 200 || status > 299 {
		return fmt.Sprintf("GitHub API error %d: %s", status, safeSnippet(string(raw))), nil
	}

	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Path string `json:"path"`
		Size int    `json:"size"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		// might be a single file
		var single struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Path string `json:"path"`
		}
		if err2 := json.Unmarshal(raw, &single); err2 != nil {
			return "Failed to parse directory listing", nil
		}
		return fmt.Sprintf("(single file) %s [%s]", single.Path, single.Type), nil
	}

	var sb strings.Builder
	label := dirPath
	if label == "" {
		label = "/"
	}
	sb.WriteString(fmt.Sprintf("Directory: %s (%d items)\n", label, len(entries)))
	for _, entry := range entries {
		icon := "📄"
		if entry.Type == "dir" {
			icon = "📁"
		}
		if entry.Type == "dir" {
			sb.WriteString(fmt.Sprintf("%s %s/\n", icon, entry.Path))
		} else {
			sb.WriteString(fmt.Sprintf("%s %s (%d bytes)\n", icon, entry.Path, entry.Size))
		}
	}
	return sb.String(), nil
}

// ── read_file ─────────────────────────────────────────────────────────────────

func (e *Executor) readFile(ctx context.Context, filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("path is required")
	}
	filePath = sanitizePath(filePath)
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
		url.PathEscape(e.repo.Owner), url.PathEscape(e.repo.Repo),
		escapeContentPath(filePath), url.QueryEscape(e.repo.Branch))

	raw, status, err := e.ghGET(ctx, apiURL)
	if err != nil {
		return "", err
	}
	if status == 404 {
		return fmt.Sprintf("File '%s' not found", filePath), nil
	}
	if status < 200 || status > 299 {
		return fmt.Sprintf("GitHub API error %d: %s", status, safeSnippet(string(raw))), nil
	}

	var ghFile struct {
		Name    string `json:"name"`
		Content string `json:"content"`
		Size    int    `json:"size"`
	}
	if err := json.Unmarshal(raw, &ghFile); err != nil {
		return "Failed to parse file response", nil
	}
	if ghFile.Size > 500*1024 { // 500KB cap
		return fmt.Sprintf("File '%s' is too large (%d bytes). Only files under 500KB can be read.", filePath, ghFile.Size), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(ghFile.Content, "\n", ""))
	if err != nil {
		return fmt.Sprintf("Failed to decode file content: %v", err), nil
	}
	content := string(decoded)
	// Truncate very long files for context window safety
	const maxChars = 50000
	if len(content) > maxChars {
		content = content[:maxChars] + fmt.Sprintf("\n\n[... truncated, total %d bytes ...]", ghFile.Size)
	}
	return fmt.Sprintf("File: %s\n---\n%s", filePath, content), nil
}

// ── write_file ────────────────────────────────────────────────────────────────

// dangerousPrefixes lists path prefixes that the agent should never write to.
// #fix5: prevents AI prompt injection from writing CI/CD or security-sensitive files.
var dangerousPrefixes = []string{
	".github/workflows",
	".github/actions",
	".ssh",
	".gnupg",
	"Makefile",
}

func (e *Executor) writeFile(ctx context.Context, filePath, content, commitMsg string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("path is required")
	}
	if commitMsg == "" {
		commitMsg = "chore: update " + filepath.Base(filePath)
	}
	filePath = sanitizePath(filePath)
	// #fix5: block writes to dangerous paths
	lp := strings.ToLower(filePath)
	for _, prefix := range dangerousPrefixes {
		if lp == strings.ToLower(prefix) || strings.HasPrefix(lp, strings.ToLower(prefix)+"/") {
			return fmt.Sprintf("write_file: path '%s' is restricted for security reasons. Modifying CI/CD workflows or credential files is not allowed.", filePath), nil
		}
	}

	// Get existing SHA (needed for updates)
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s",
		url.PathEscape(e.repo.Owner), url.PathEscape(e.repo.Repo),
		escapeContentPath(filePath))

	existingSHA := ""
	raw, status, err := e.ghGET(ctx, apiURL+"?ref="+url.QueryEscape(e.repo.Branch))
	if err != nil {
		return "", err
	}
	if status == 200 {
		var existing struct {
			SHA string `json:"sha"`
		}
		_ = json.Unmarshal(raw, &existing)
		existingSHA = existing.SHA
	}

	// Build PUT payload
	payload := map[string]any{
		"message": commitMsg,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  e.repo.Branch,
	}
	if existingSHA != "" {
		payload["sha"] = existingSHA
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("build request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.repo.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ZPWU-CODE-agent")

	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub request failed: %w", err)
	}
	defer resp.Body.Close()
	respRaw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Sprintf("Failed to write file: GitHub returned %d: %s", resp.StatusCode, safeSnippet(string(respRaw))), nil
	}

	var result struct {
		Commit struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
		} `json:"commit"`
	}
	_ = json.Unmarshal(respRaw, &result)

	verb := "Created"
	if existingSHA != "" {
		verb = "Updated"
	}
	return fmt.Sprintf("%s '%s' successfully. Commit: %s\nURL: %s",
		verb, filePath, result.Commit.SHA[:min(7, len(result.Commit.SHA))], result.Commit.HTMLURL), nil
}

// ── search_files ──────────────────────────────────────────────────────────────

func (e *Executor) searchFiles(ctx context.Context, query string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	// GitHub code search: q=<query>+repo:<owner>/<repo>
	fullQuery := fmt.Sprintf("%s repo:%s/%s", query, e.repo.Owner, e.repo.Repo)
	apiURL := "https://api.github.com/search/code?q=" + url.QueryEscape(fullQuery) + "&per_page=20"

	raw, status, err := e.ghGET(ctx, apiURL)
	if err != nil {
		return "", err
	}
	if status == 422 {
		return "Search query invalid or repository too new for indexing.", nil
	}
	if status < 200 || status > 299 {
		return fmt.Sprintf("Search failed: GitHub returned %d: %s", status, safeSnippet(string(raw))), nil
	}

	var result struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			Path    string `json:"path"`
			HTMLURL string `json:"html_url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "Failed to parse search results", nil
	}
	if result.TotalCount == 0 {
		return fmt.Sprintf("No files found matching '%s'", query), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d file(s) matching '%s':\n", result.TotalCount, query))
	for _, item := range result.Items {
		sb.WriteString(fmt.Sprintf("  - %s\n", item.Path))
	}
	return sb.String(), nil
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (e *Executor) ghGET(ctx context.Context, apiURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.repo.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ZPWU-CODE-agent")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GitHub request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return raw, resp.StatusCode, nil
}

// ─── Utility ─────────────────────────────────────────────────────────────────

func sanitizePath(p string) string {
	p = strings.TrimSpace(strings.TrimPrefix(p, "/"))
	if p == "." || p == "" {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(p))
	// Block path traversal: reject any path that escapes the repo root.
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func escapeContentPath(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func safeSnippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
