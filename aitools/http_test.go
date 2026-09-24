package aitools

import (
	"context"
	"net/http"
	"testing"
)

func TestSetIdempotencyHeaderFromInvocationMetadata(t *testing.T) {
	t.Parallel()

	ctx := WithInvocationMetadata(context.Background(), InvocationMetadata{IdempotencyKey: "durable-key"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	setIdempotencyHeader(req)
	if got := req.Header.Get("Idempotency-Key"); got != "durable-key" {
		t.Fatalf("Idempotency-Key = %q, want durable-key", got)
	}
}

func TestSetIdempotencyHeaderPreservesExplicitValue(t *testing.T) {
	t.Parallel()

	ctx := WithInvocationMetadata(context.Background(), InvocationMetadata{IdempotencyKey: "durable-key"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("Idempotency-Key", "caller-key")
	setIdempotencyHeader(req)
	if got := req.Header.Get("Idempotency-Key"); got != "caller-key" {
		t.Fatalf("Idempotency-Key = %q, want caller-key", got)
	}
}
