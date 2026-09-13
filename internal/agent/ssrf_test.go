package agent

import (
	"strings"
	"testing"
)

func TestValidateLLMBaseURL_DefaultPolicy(t *testing.T) {
	SetSSRFStrict(false)
	t.Cleanup(func() { SetSSRFStrict(false) })

	allowed := []string{
		"",                             // empty → caller supplies default
		"https://api.openai.com/v1",    // public https
		"http://api.openai.com/v1",     // public http (allowed, not ideal)
		"http://localhost:11434/v1",    // local Ollama gateway (self-hosted default)
		"http://127.0.0.1:8000/v1",     // local vLLM
		"http://192.168.1.10:11434/v1", // LAN inference host
		"http://10.0.0.5/v1",           // private 10/8 (still allowed by default)
	}
	for _, u := range allowed {
		if err := ValidateLLMBaseURL(u); err != nil {
			t.Errorf("expected %q to be allowed under default policy, got: %v", u, err)
		}
	}

	blocked := []string{
		"http://169.254.169.254/latest/meta-data/", // AWS/GCP metadata
		"http://[fe80::1]/",                        // IPv6 link-local
		"http://metadata.google.internal/",         // GCP metadata hostname
		"http://0.0.0.0:9999/",                     // unspecified IPv4
		"http://[::]/",                             // unspecified IPv6
		"http://[ff02::1]/",                        // IPv6 multicast
		"ftp://example.com/v1",                     // non-http scheme
		"not a url",                                // garbage
		"https://",                                 // no host
	}
	for _, u := range blocked {
		err := ValidateLLMBaseURL(u)
		if err == nil {
			t.Errorf("expected %q to be blocked under default policy, got nil", u)
			continue
		}
		// Basic sanity: message mentions what's happening.
		if !strings.Contains(err.Error(), "not allowed") &&
			!strings.Contains(err.Error(), "scheme") &&
			!strings.Contains(err.Error(), "valid") &&
			!strings.Contains(err.Error(), "no host") {
			t.Errorf("unexpected error text for %q: %v", u, err)
		}
	}
}

func TestValidateLLMBaseURL_StrictPolicy(t *testing.T) {
	SetSSRFStrict(true)
	t.Cleanup(func() { SetSSRFStrict(false) })

	// Loopback + private ranges are now rejected.
	blocked := []string{
		"http://localhost:11434/v1",
		"http://127.0.0.1:8000/v1",
		"http://192.168.1.10:11434/v1",
		"http://10.0.0.5/v1",
		"http://172.16.0.1/v1",
		"http://[::1]/v1",
		"http://[fc00::1]/v1",
	}
	for _, u := range blocked {
		if err := ValidateLLMBaseURL(u); err == nil {
			t.Errorf("expected %q to be blocked under strict policy, got nil", u)
		}
	}

	// Public endpoints remain allowed.
	allowed := []string{
		"https://api.openai.com/v1",
		"https://api.anthropic.com",
	}
	for _, u := range allowed {
		if err := ValidateLLMBaseURL(u); err != nil {
			t.Errorf("expected %q to be allowed under strict policy, got: %v", u, err)
		}
	}
}

func TestNormalizeOpenAIEndpointRejectsMetadata(t *testing.T) {
	SetSSRFStrict(false)
	if _, err := NormalizeOpenAIEndpoint("http://169.254.169.254/latest/"); err == nil {
		t.Fatalf("expected NormalizeOpenAIEndpoint to reject 169.254.169.254")
	}
}

func TestNormalizeOpenAIEndpointAcceptsLocalGateway(t *testing.T) {
	SetSSRFStrict(false)
	got, err := NormalizeOpenAIEndpoint("http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("expected localhost endpoint allowed by default, got: %v", err)
	}
	if got != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("unexpected normalized endpoint: %s", got)
	}
}

func TestTruncateForDisplayIsRuneSafe(t *testing.T) {
	in := "中文测试 abcdef 日本語"
	out := truncateForDisplay(in, 4)
	if out != "中文测试…" {
		t.Fatalf("expected first 4 runes + ellipsis, got %q", out)
	}
	// No truncation when within budget.
	if got := truncateForDisplay("短", 10); got != "短" {
		t.Fatalf("expected no change, got %q", got)
	}
}
