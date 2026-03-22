package llm

import (
	"fmt"
	"testing"
)

func newTestSession(msgCount int) *Session {
	s := &Session{
		messages: make([]Message, msgCount),
	}
	for i := 0; i < msgCount; i++ {
		role := RoleUser
		if i%2 == 1 {
			role = RoleAssistant
		}
		s.messages[i] = Message{
			Role:    role,
			Content: fmt.Sprintf("msg-%d", i),
		}
	}
	return s
}

func TestNewPruningManager(t *testing.T) {
	s := newTestSession(0)
	pm := NewPruningManager(s, 10, 5)

	if pm.session != s {
		t.Fatal("session not set")
	}
	if pm.pruneOn != 10 {
		t.Fatalf("pruneOn = %d, want 10", pm.pruneOn)
	}
	if pm.pruneTo != 5 {
		t.Fatalf("pruneTo = %d, want 5", pm.pruneTo)
	}
}

func TestApplyTurnPruning_BelowThreshold(t *testing.T) {
	// pruneOn=5 means trigger at 10 messages; we have 8
	s := newTestSession(8)
	pm := NewPruningManager(s, 5, 3)

	dropped := pm.ApplyTurnPruning()
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0 (below threshold)", dropped)
	}
	if len(s.messages) != 8 {
		t.Fatalf("message count = %d, want 8", len(s.messages))
	}
}

func TestApplyTurnPruning_ExactlyAtThreshold(t *testing.T) {
	// pruneOn=5 means trigger at 10 messages; we have exactly 10
	s := newTestSession(10)
	pm := NewPruningManager(s, 5, 3)

	dropped := pm.ApplyTurnPruning()
	// Should prune: 10 - (3*2) = 4 dropped
	if dropped != 4 {
		t.Fatalf("dropped = %d, want 4", dropped)
	}
	if len(s.messages) != 6 {
		t.Fatalf("message count = %d, want 6", len(s.messages))
	}
}

func TestApplyTurnPruning_AboveThreshold(t *testing.T) {
	// pruneOn=5 means trigger at 10 messages; we have 14
	s := newTestSession(14)
	pm := NewPruningManager(s, 5, 3)

	dropped := pm.ApplyTurnPruning()
	// Should prune: 14 - 6 = 8 dropped
	if dropped != 8 {
		t.Fatalf("dropped = %d, want 8", dropped)
	}
	if len(s.messages) != 6 {
		t.Fatalf("message count = %d, want 6", len(s.messages))
	}
}

func TestApplyTurnPruning_Disabled(t *testing.T) {
	s := newTestSession(20)
	pm := NewPruningManager(s, 0, 3)

	dropped := pm.ApplyTurnPruning()
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0 (disabled)", dropped)
	}
	if len(s.messages) != 20 {
		t.Fatalf("message count = %d, want 20", len(s.messages))
	}
}

func TestApplyTurnPruning_NegativePruneOn(t *testing.T) {
	s := newTestSession(20)
	pm := NewPruningManager(s, -1, 3)

	dropped := pm.ApplyTurnPruning()
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0 (negative pruneOn)", dropped)
	}
}

func TestApplyTurnPruning_PreservesRecentMessages(t *testing.T) {
	// Verify the *most recent* messages are kept
	s := newTestSession(10)
	pm := NewPruningManager(s, 5, 2) // prune to 4 messages

	dropped := pm.ApplyTurnPruning()
	if dropped != 6 {
		t.Fatalf("dropped = %d, want 6", dropped)
	}
	if len(s.messages) != 4 {
		t.Fatalf("message count = %d, want 4", len(s.messages))
	}

	// The remaining messages should be the last 4 (indices 6,7,8,9 of original)
	for i, msg := range s.messages {
		expected := fmt.Sprintf("msg-%d", i+6)
		if msg.Content != expected {
			t.Fatalf("message[%d].Content = %q, want %q", i, msg.Content, expected)
		}
	}
}

func TestApplyTurnPruning_MultipleCalls(t *testing.T) {
	// After pruning, below threshold => second call is no-op
	s := newTestSession(10)
	pm := NewPruningManager(s, 5, 3)

	dropped1 := pm.ApplyTurnPruning()
	if dropped1 != 4 {
		t.Fatalf("first call: dropped = %d, want 4", dropped1)
	}

	dropped2 := pm.ApplyTurnPruning()
	if dropped2 != 0 {
		t.Fatalf("second call: dropped = %d, want 0", dropped2)
	}
}
