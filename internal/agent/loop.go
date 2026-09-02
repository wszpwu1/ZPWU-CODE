// Package agent — Agent Loop with tool calling for OpenAI-compatible and
// Claude (Anthropic) APIs. Progress is streamed via SSE.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ─── SSE event types ──────────────────────────────────────────────────────────

// EventType classifies SSE events sent to the browser.
type EventType string

const (
	EventThinking   EventType = "thinking"      // AI is reasoning / generating
	EventToolCall   EventType = "tool_call"     // AI requested a tool
	EventToolResult EventType = "tool_result"   // tool executed, result ready
	EventText       EventType = "text"          // final AI text
	EventDone       EventType = "done"          // agent loop finished
	EventError      EventType = "error"         // fatal error
	EventApproval   EventType = "tool_approval" // waiting for user to approve tool execution
	EventApproved   EventType = "tool_approved" // user approved, executing
	EventRejected   EventType = "tool_rejected" // user rejected
)

// Event is serialised to SSE data lines.
type Event struct {
	Type    EventType `json:"type"`
	Content string    `json:"content,omitempty"` // text or tool result
	Tool    string    `json:"tool,omitempty"`    // tool name
	CallID  string    `json:"call_id,omitempty"` // unique tool call id (fix #1)
	Input   string    `json:"input,omitempty"`   // tool arguments (JSON string)
	Round   int       `json:"round,omitempty"`   // loop iteration
	// Approval fields
	FilePath      string `json:"file_path,omitempty"`
	CommitMessage string `json:"commit_message,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
}

// Emitter sends SSE events.
type Emitter interface {
	Emit(e Event)
}

// ─── Approver interface ───────────────────────────────────────────────────────

// ApprovalDecision is the user's answer to a tool-approval request.
type ApprovalDecision struct {
	Approved bool
	Reason   string // optional rejection reason
}

// Approver gates write_file execution behind user confirmation.
// Implementations block until the user responds (or ctx is cancelled).
type Approver interface {
	// RequestApproval sends an approval event via SSE and blocks until the
	// user responds or ctx is done. Returns true if user approved.
	RequestApproval(ctx context.Context, callID, filePath, content, commitMsg string) (bool, error)
}

// NoopApprover always approves immediately (used when no approver is wired in).
type NoopApprover struct{}

func (NoopApprover) RequestApproval(_ context.Context, _, _, _, _ string) (bool, error) {
	return true, nil
}

// ─── Agent run request ────────────────────────────────────────────────────────

// RunRequest holds everything needed to start an agent loop.
type RunRequest struct {
	ProviderKind string
	BaseURL      string
	Model        string
	APIKey       string
	ExtraHeaders map[string]string
	System       string
	History      []Message
	UserMessage  string
	Repo         RepoCtx
	MaxRounds    int      // default 10
	Approver     Approver // gates write_file; nil → NoopApprover
}

// Message is a conversation turn.
type Message struct {
	Role       string     `json:"role"`    // "user" | "assistant" | "tool"
	Content    string     `json:"content"` // text content
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ─── normalizeOpenAIEndpoint ──────────────────────────────────────────────────
// Single canonical implementation — eliminates the duplicate in handlers.go.
// #fix9 #fix10: strips existing suffix before appending to prevent /v1/v1 duplication.

func NormalizeOpenAIEndpoint(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", errors.New("base_url is required for OpenAI-compatible providers")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("base_url must be a valid absolute URL")
	}
	// Strip trailing slashes and any known endpoint suffixes first.
	trimmed := strings.TrimRight(base, "/")
	trimmed = strings.TrimSuffix(trimmed, "/v1/chat/completions")
	trimmed = strings.TrimSuffix(trimmed, "/chat/completions")
	// Trim everything after the last /v1 segment so that non-standard proxy
	// paths like /proxy/v1/completions don't produce double /v1 segments.
	if idx := strings.LastIndex(trimmed, "/v1"); idx != -1 {
		trimmed = trimmed[:idx]
	}
	return trimmed + "/v1/chat/completions", nil
}

// ─── Agent Loop ───────────────────────────────────────────────────────────────

// Run executes the agent loop, streaming events via emitter.
func Run(ctx context.Context, req RunRequest, emitter Emitter) ([]Message, error) {
	if req.MaxRounds <= 0 {
		req.MaxRounds = 10
	}
	approver := req.Approver
	if approver == nil {
		approver = NoopApprover{}
	}
	executor := NewExecutor(req.Repo)
	client := &http.Client{Timeout: 90 * time.Second}

	history := make([]Message, len(req.History))
	copy(history, req.History)
	history = append(history, Message{Role: "user", Content: req.UserMessage})

	kind := strings.ToLower(strings.TrimSpace(req.ProviderKind))

	for round := 1; round <= req.MaxRounds; round++ {
		emitter.Emit(Event{Type: EventThinking, Content: fmt.Sprintf("第 %d 轮推理中…", round), Round: round})

		var reply string
		var toolCalls []ToolCall
		var err error

		if kind == "claude" {
			reply, toolCalls, err = callClaude(ctx, client, req, history)
		} else {
			reply, toolCalls, err = callOpenAI(ctx, client, req, history)
		}

		if err != nil {
			emitter.Emit(Event{Type: EventError, Content: err.Error()})
			return history, err
		}

		// Append assistant turn to history
		history = append(history, Message{Role: "assistant", Content: reply, ToolCalls: toolCalls})

		// No tool calls → done
		if len(toolCalls) == 0 {
			emitter.Emit(Event{Type: EventText, Content: reply, Round: round})
			emitter.Emit(Event{Type: EventDone, Content: fmt.Sprintf("完成（共 %d 轮）", round)})
			return history, nil
		}

		// Emit thinking text alongside tool calls
		if strings.TrimSpace(reply) != "" {
			emitter.Emit(Event{Type: EventThinking, Content: reply, Round: round})
		}

		// Execute tool calls sequentially
		for _, tc := range toolCalls {
			argsDisplay := tc.Function.Arguments
			if len(argsDisplay) > 300 {
				argsDisplay = argsDisplay[:300] + "…"
			}

			// ── write_file: gate behind user approval (approver emits the SSE event) ──
			if tc.Function.Name == "write_file" {
				var args map[string]string
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				// NOTE: do NOT emit EventApproval here — sseApprover.RequestApproval
				// already emits it. Emitting twice would render two cards in the UI.
				approved, approveErr := approver.RequestApproval(
					ctx, tc.ID,
					args["path"], args["content"], args["commit_message"],
				)
				if approveErr != nil {
					emitter.Emit(Event{Type: EventError, Content: "授权等待超时或连接断开"})
					return history, approveErr
				}
				if !approved {
					result := fmt.Sprintf("用户拒绝了对 '%s' 的写入操作。", args["path"])
					emitter.Emit(Event{
						Type: EventRejected, Tool: tc.Function.Name,
						CallID: tc.ID, Content: result, Round: round,
					})
					history = append(history, Message{Role: "tool", Content: result, ToolCallID: tc.ID})
					continue
				}
				emitter.Emit(Event{
					Type: EventApproved, Tool: tc.Function.Name,
					CallID: tc.ID, Content: "用户已授权，正在写入…", Round: round,
				})
			} else {
				// Non-write tools: emit tool_call event as before
				emitter.Emit(Event{
					Type:   EventToolCall,
					Tool:   tc.Function.Name,
					CallID: tc.ID,
					Input:  argsDisplay,
					Round:  round,
				})
			}

			result, execErr := executor.Execute(ctx, tc)
			if execErr != nil {
				result = fmt.Sprintf("Tool execution error: %v", execErr)
			}

			resultSnippet := result
			if len(resultSnippet) > 400 {
				resultSnippet = resultSnippet[:400] + "…"
			}
			emitter.Emit(Event{
				Type:    EventToolResult,
				Tool:    tc.Function.Name,
				CallID:  tc.ID,
				Content: resultSnippet,
				Round:   round,
			})

			// #fix2: always append tool result as role=tool with ToolCallID
			history = append(history, Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	emitter.Emit(Event{Type: EventDone, Content: fmt.Sprintf("已达最大轮数 %d，任务结束", req.MaxRounds)})
	return history, nil
}

// ─── OpenAI-compatible ────────────────────────────────────────────────────────

func callOpenAI(ctx context.Context, client *http.Client, req RunRequest, history []Message) (string, []ToolCall, error) {
	// #fix10: use the single canonical normalizer
	endpoint, err := NormalizeOpenAIEndpoint(req.BaseURL)
	if err != nil {
		return "", nil, err
	}

	msgs := make([]map[string]any, 0, len(history)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range history {
		switch m.Role {
		case "user":
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Content})
		case "assistant":
			am := map[string]any{"role": "assistant", "content": m.Content}
			if len(m.ToolCalls) > 0 {
				am["tool_calls"] = openAIToolCalls(m.ToolCalls)
			}
			msgs = append(msgs, am)
		case "tool":
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": m.ToolCallID,
				"content":      m.Content,
			})
		}
	}

	payload := map[string]any{
		"model":    req.Model,
		"messages": msgs,
		"tools":    AllTools,
		"stream":   false,
	}
	bodyBytes, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	for k, v := range req.ExtraHeaders {
		if !strings.EqualFold(k, "Authorization") && !strings.EqualFold(k, "Content-Type") {
			httpReq.Header.Set(k, v)
		}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, safeSnippet(string(raw)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", nil, fmt.Errorf("parse response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", nil, errors.New("upstream returned empty choices")
	}
	msg := parsed.Choices[0].Message
	return strings.TrimSpace(msg.Content), msg.ToolCalls, nil
}

func openAIToolCalls(tcs []ToolCall) []map[string]any {
	out := make([]map[string]any, len(tcs))
	for i, tc := range tcs {
		out[i] = map[string]any{
			"id":   tc.ID,
			"type": "function",
			"function": map[string]any{
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
			},
		}
	}
	return out
}

// ─── Claude (Anthropic Messages API) ─────────────────────────────────────────
// #fix2: properly converts tool role messages to Claude's user/tool_result blocks.

func callClaude(ctx context.Context, client *http.Client, req RunRequest, history []Message) (string, []ToolCall, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	endpoint := baseURL + "/v1/messages"

	model := req.Model
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	// Convert tool defs to Anthropic format
	claudeTools := make([]map[string]any, len(AllTools))
	for i, t := range AllTools {
		claudeTools[i] = map[string]any{
			"name":        t.Function.Name,
			"description": t.Function.Description,
			"input_schema": map[string]any{
				"type":       t.Function.Parameters.Type,
				"properties": t.Function.Parameters.Properties,
				"required":   t.Function.Parameters.Required,
			},
		}
	}

	// #fix2: Convert history to Anthropic format.
	// Claude requires:
	//   - role=assistant with tool_use content blocks
	//   - immediately followed by role=user with tool_result content blocks
	// Orphan role=tool messages that appear without a preceding assistant-with-tool-calls
	// are now properly collected and attached.
	msgs := make([]map[string]any, 0, len(history))
	i := 0
	for i < len(history) {
		m := history[i]
		switch m.Role {
		case "user":
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Content})
			i++
		case "assistant":
			if len(m.ToolCalls) == 0 {
				msgs = append(msgs, map[string]any{"role": "assistant", "content": m.Content})
				i++
			} else {
				// Build assistant content blocks: optional text + tool_use blocks
				contentBlocks := make([]map[string]any, 0)
				if m.Content != "" {
					contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": m.Content})
				}
				for _, tc := range m.ToolCalls {
					var inputMap map[string]any
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &inputMap)
					if inputMap == nil {
						inputMap = map[string]any{}
					}
					contentBlocks = append(contentBlocks, map[string]any{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Function.Name,
						"input": inputMap,
					})
				}
				msgs = append(msgs, map[string]any{"role": "assistant", "content": contentBlocks})
				i++

				// Collect all consecutive tool results (#fix2: was skipping orphan tool msgs)
				resultBlocks := make([]map[string]any, 0)
				for i < len(history) && history[i].Role == "tool" {
					tr := history[i]
					resultBlocks = append(resultBlocks, map[string]any{
						"type":        "tool_result",
						"tool_use_id": tr.ToolCallID,
						"content":     tr.Content,
					})
					i++
				}
				if len(resultBlocks) > 0 {
					msgs = append(msgs, map[string]any{"role": "user", "content": resultBlocks})
				}
			}
		case "tool":
			// Orphan tool message without preceding assistant-tool-calls.
			// Wrap as a standalone user message with tool_result block.
			msgs = append(msgs, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     m.Content,
				}},
			})
			i++
		default:
			i++
		}
	}

	payload := map[string]any{
		"model":      model,
		"max_tokens": 8192,
		"tools":      claudeTools,
		"messages":   msgs,
	}
	if req.System != "" {
		payload["system"] = req.System
	}
	bodyBytes, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", req.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range req.ExtraHeaders {
		lk := strings.ToLower(k)
		if lk != "x-api-key" && lk != "content-type" && lk != "anthropic-version" {
			httpReq.Header.Set(k, v)
		}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("Claude request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", nil, fmt.Errorf("Claude returned %d: %s", resp.StatusCode, safeSnippet(string(raw)))
	}

	var parsed struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text,omitempty"`
			ID    string         `json:"id,omitempty"`
			Name  string         `json:"name,omitempty"`
			Input map[string]any `json:"input,omitempty"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", nil, fmt.Errorf("parse Claude response: %w", err)
	}

	var textParts []string
	var toolCalls []ToolCall
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			inputJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      block.Name,
					Arguments: string(inputJSON),
				},
			})
		}
	}
	return strings.Join(textParts, "\n"), toolCalls, nil
}

// ─── SSE writer ───────────────────────────────────────────────────────────────

// SSEEmitter writes events to an http.ResponseWriter as Server-Sent Events.
type SSEEmitter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEEmitter creates an SSEEmitter and sets SSE response headers.
func NewSSEEmitter(w http.ResponseWriter) (*SSEEmitter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return &SSEEmitter{w: w, flusher: flusher}, true
}

// Emit serialises an event as SSE and flushes immediately.
func (e *SSEEmitter) Emit(ev Event) {
	data, _ := json.Marshal(ev)
	fmt.Fprintf(e.w, "data: %s\n\n", data)
	e.flusher.Flush()
}

// ─── AgentHTTPRequest ─────────────────────────────────────────────────────────

// AgentHTTPRequest is the JSON body for POST /api/agent/run.
type AgentHTTPRequest struct {
	Message  string     `json:"message"`
	System   string     `json:"system"`
	History  []Message  `json:"history"`
	Provider ProviderIn `json:"provider"`
	Owner    string     `json:"owner"`
	Repo     string     `json:"repo"`
	Branch   string     `json:"branch"`
}

// ProviderIn carries provider credentials from the browser.
type ProviderIn struct {
	Kind      string            `json:"kind"`
	BaseURL   string            `json:"base_url"`
	Model     string            `json:"model"`
	APIKey    string            `json:"api_key"`
	Headers   map[string]string `json:"headers"`
	MaxRounds int               `json:"max_rounds"`
}

// BuildRunRequest converts an AgentHTTPRequest to a RunRequest.
func (a AgentHTTPRequest) BuildRunRequest(ghToken string) RunRequest {
	branch := a.Branch
	if branch == "" {
		branch = "main"
	}
	maxRounds := a.Provider.MaxRounds
	if maxRounds <= 0 || maxRounds > 20 {
		maxRounds = 10
	}
	return RunRequest{
		ProviderKind: a.Provider.Kind,
		BaseURL:      a.Provider.BaseURL,
		Model:        a.Provider.Model,
		APIKey:       a.Provider.APIKey,
		ExtraHeaders: a.Provider.Headers,
		System:       a.System,
		History:      a.History,
		UserMessage:  a.Message,
		Repo: RepoCtx{
			Owner:  a.Owner,
			Repo:   a.Repo,
			Branch: branch,
			Token:  ghToken,
		},
		MaxRounds: maxRounds,
	}
}
