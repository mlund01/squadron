package aitools

import (
	"context"
	"testing"
)

func TestStableIdempotencyKey(t *testing.T) {
	t.Parallel()

	got := StableIdempotencyKey("run-1", "attempt-1", "tool-1")
	if got != StableIdempotencyKey("run-1", "attempt-1", "tool-1") {
		t.Fatal("StableIdempotencyKey() changed for identical invocation identity")
	}
	if got == StableIdempotencyKey("run-1", "attempt-1", "tool-2") {
		t.Fatal("StableIdempotencyKey() collided for different tool-use IDs")
	}
	if len(got) != 67 || got[:3] != "sq_" {
		t.Fatalf("StableIdempotencyKey() = %q, want sq_ plus a SHA-256 digest", got)
	}
}

func TestInvocationMetadataContextRoundTrip(t *testing.T) {
	t.Parallel()

	want := InvocationMetadata{RunID: "run-1", TaskName: "collect", AttemptID: "attempt-1", ToolUseID: "tool-1", IdempotencyKey: "key-1"}
	ctx := WithInvocationMetadata(context.Background(), want)
	got, ok := InvocationMetadataFromContext(ctx)
	if !ok || got != want {
		t.Fatalf("InvocationMetadataFromContext() = %#v, %v; want %#v, true", got, ok, want)
	}
	if _, ok := InvocationMetadataFromContext(context.Background()); ok {
		t.Fatal("InvocationMetadataFromContext() found metadata on a plain context")
	}
}
