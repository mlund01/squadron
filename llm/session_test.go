package llm

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// failingProvider returns errors for the first N calls, then succeeds.
type failingProvider struct {
	failCount    int
	callCount    int
	statusCode   int
	lastResponse *ChatResponse
}

func (p *failingProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *failingProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	p.callCount++
	if p.callCount <= p.failCount {
		return nil, fmt.Errorf("POST \"https://api.example.com/v1/chat\": %d %s",
			p.statusCode, statusText(p.statusCode))
	}
	ch := make(chan StreamChunk, 2)
	ch <- StreamChunk{Content: "ok", Done: false}
	ch <- StreamChunk{Done: true, Usage: &Usage{InputTokens: 10, OutputTokens: 5}}
	close(ch)
	return ch, nil
}

func statusText(code int) string {
	switch code {
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 503:
		return "Service Unavailable"
	default:
		return "Error"
	}
}

func TestRetry_429Recovery(t *testing.T) {
	provider := &failingProvider{failCount: 2, statusCode: 429}
	session := NewSession(provider, "test-model", "system prompt")

	resp, err := session.SendStream(context.Background(), "hello", nil)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("expected content 'ok', got %q", resp.Content)
	}
	if provider.callCount != 3 {
		t.Fatalf("expected 3 calls (2 failures + 1 success), got %d", provider.callCount)
	}
}

func TestRetry_500Recovery(t *testing.T) {
	provider := &failingProvider{failCount: 1, statusCode: 500}
	session := NewSession(provider, "test-model", "system prompt")

	resp, err := session.SendStream(context.Background(), "hello", nil)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("expected content 'ok', got %q", resp.Content)
	}
	if provider.callCount != 2 {
		t.Fatalf("expected 2 calls (1 failure + 1 success), got %d", provider.callCount)
	}
}

func TestRetry_NonRetryableError(t *testing.T) {
	provider := &failingProvider{failCount: 100, statusCode: 400}
	session := NewSession(provider, "test-model", "system prompt")

	_, err := session.SendStream(context.Background(), "hello", nil)
	if err == nil {
		t.Fatal("expected error for non-retryable status")
	}
	if provider.callCount != 1 {
		t.Fatalf("expected 1 call (no retry for 400), got %d", provider.callCount)
	}
}

func TestRetry_ContextCancellation(t *testing.T) {
	provider := &failingProvider{failCount: 100, statusCode: 429}
	session := NewSession(provider, "test-model", "system prompt")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := session.SendStream(ctx, "hello", nil)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	if provider.callCount < 1 {
		t.Fatal("expected at least 1 call")
	}
}

func TestRetry_ExponentialBackoff(t *testing.T) {
	provider := &failingProvider{failCount: 3, statusCode: 503}
	session := NewSession(provider, "test-model", "system prompt")

	start := time.Now()
	resp, err := session.SendStream(context.Background(), "hello", nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("expected content 'ok', got %q", resp.Content)
	}
	// 3 retries: 5s + 10s + 20s = 35s minimum
	if elapsed < 30*time.Second {
		t.Fatalf("expected at least 30s of backoff, got %s", elapsed)
	}
	if provider.callCount != 4 {
		t.Fatalf("expected 4 calls (3 failures + 1 success), got %d", provider.callCount)
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		msg       string
		retryable bool
	}{
		{`POST "https://api.openai.com/v1/chat": 429 Too Many Requests`, true},
		{`POST "https://api.openai.com/v1/chat": 500 Internal Server Error`, true},
		{`POST "https://api.openai.com/v1/chat": 502 Bad Gateway`, true},
		{`POST "https://api.openai.com/v1/chat": 503 Service Unavailable`, true},
		{`POST "https://api.openai.com/v1/chat": 504 Gateway Timeout`, true},
		{`POST "https://api.anthropic.com/v1/messages": 529 Overloaded`, true},
		{`POST "https://api.openai.com/v1/chat": 400 Bad Request`, false},
		{`POST "https://api.openai.com/v1/chat": 401 Unauthorized`, false},
		{`POST "https://api.openai.com/v1/chat": 403 Forbidden`, false},
		{`connection refused`, false},
	}

	for _, tt := range tests {
		err := fmt.Errorf("%s", tt.msg)
		got := isRetryableError(err)
		if got != tt.retryable {
			t.Errorf("isRetryableError(%q) = %v, want %v", tt.msg, got, tt.retryable)
		}
	}
}
