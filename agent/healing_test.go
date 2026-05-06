package agent

import (
	"context"
	"testing"
)

func TestMaybeInterrupted_PassesThroughWhenCtxAlive(t *testing.T) {
	ctx := context.Background()
	got := MaybeInterrupted(ctx, "real result body")
	if got != "real result body" {
		t.Fatalf("expected pass-through, got %q", got)
	}
}

func TestMaybeInterrupted_SubstitutesWhenCtxCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := MaybeInterrupted(ctx, `Error: request failed - Get "https://x": context canceled`)
	if got != InterruptedToolMessage {
		t.Fatalf("expected InterruptedToolMessage, got %q", got)
	}
}

func TestMaybeInterrupted_SubstitutesWhenCtxDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	got := MaybeInterrupted(ctx, "anything")
	if got != InterruptedToolMessage {
		t.Fatalf("expected InterruptedToolMessage on deadline, got %q", got)
	}
}

func TestInterruptionMessages_AreDistinct(t *testing.T) {
	if InterruptedToolMessage == QueuedToolMessage {
		t.Fatal("Interrupted and Queued messages should be distinguishable so the LLM knows which calls had side effects")
	}
	if InterruptedToolMessage == "" || QueuedToolMessage == "" {
		t.Fatal("messages must be non-empty")
	}
}
