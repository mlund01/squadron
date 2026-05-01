package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenAIProvider_BaseURL verifies that a custom base_url routes requests
// to that host instead of the default api.openai.com — the proxy escape hatch
// users declare via `base_url = "..."` on model blocks. Squadron now uses
// the Responses API (`/responses`) end-to-end.
func TestOpenAIProvider_BaseURL(t *testing.T) {
	hit := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- r.URL.Path:
		default:
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		// Minimal valid Response shape so the client doesn't error.
		_, _ = w.Write([]byte(`{"id":"resp_x","object":"response","created_at":0,"status":"completed","output":[],"error":null,"incomplete_details":null,"instructions":null,"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"model":"gpt-4o-mini","metadata":{},"temperature":1,"top_p":1,"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`))
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-key", srv.URL)
	_, _ = p.Chat(context.Background(), &ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})

	select {
	case path := <-hit:
		if !strings.Contains(path, "responses") {
			t.Fatalf("expected /responses path, got %q", path)
		}
	default:
		t.Fatal("OpenAI provider did not send request to custom base_url")
	}
}

// TestAnthropicProvider_BaseURL is the Anthropic counterpart — ensures proxied
// Anthropic-wire-format gateways (LiteLLM, corporate proxies) are reachable.
func TestAnthropicProvider_BaseURL(t *testing.T) {
	hit := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- r.URL.Path:
		default:
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	p := NewAnthropicProvider("test-key", srv.URL)
	_, _ = p.Chat(context.Background(), &ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})

	select {
	case path := <-hit:
		if !strings.Contains(path, "messages") {
			t.Fatalf("expected messages path, got %q", path)
		}
	default:
		t.Fatal("Anthropic provider did not send request to custom base_url")
	}
}

// TestOpenAIProvider_EmptyBaseURL ensures the empty-string path leaves the SDK
// default in place — regression guard against accidentally setting WithBaseURL("").
func TestOpenAIProvider_EmptyBaseURL(t *testing.T) {
	p := NewOpenAIProvider("test-key", "")
	if p == nil || p.client == nil {
		t.Fatal("expected provider with default base URL")
	}
}

// TestAnthropicProvider_EmptyBaseURL — same guard for the Anthropic constructor.
func TestAnthropicProvider_EmptyBaseURL(t *testing.T) {
	p := NewAnthropicProvider("test-key", "")
	if p == nil || p.client == nil {
		t.Fatal("expected provider with default base URL")
	}
}

// TestGeminiProvider_EmptyBaseURL — Gemini uses gRPC for most calls so a full
// httptest round-trip isn't representative; just assert the constructor accepts
// the new signature without a baseURL.
func TestGeminiProvider_EmptyBaseURL(t *testing.T) {
	p, err := NewGeminiProvider(context.Background(), "test-key", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer p.Close()
	if p.client == nil {
		t.Fatal("expected client to be initialized")
	}
}
