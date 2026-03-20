package llm

import (
	"sync"
)

// PruningManager handles threshold-based message pruning.
// When the conversation reaches pruneOn turns, it drops oldest messages
// down to pruneTo turns. This is cache-friendly because the message
// prefix stays stable between prune events.
type PruningManager struct {
	session *Session
	mu      sync.Mutex
	pruneOn int // Trigger pruning at this many turns (0 = disabled)
	pruneTo int // Prune down to this many turns
}

// NewPruningManager creates a new pruning manager for the given session.
func NewPruningManager(session *Session, pruneOn, pruneTo int) *PruningManager {
	return &PruningManager{
		session: session,
		pruneOn: pruneOn,
		pruneTo: pruneTo,
	}
}

// ApplyTurnPruning applies threshold-based pruning if configured.
// This should be called after each LLM response.
// A turn = user message + assistant response = 2 messages.
// Returns the number of messages dropped.
func (pm *PruningManager) ApplyTurnPruning() int {
	if pm.pruneOn <= 0 {
		return 0
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()

	msgCount := len(pm.session.messages)
	pruneOnMessages := pm.pruneOn * 2
	pruneToMessages := pm.pruneTo * 2

	// Only fire when we've reached the threshold
	if msgCount < pruneOnMessages {
		return 0
	}

	// Drop oldest messages to reach prune_to
	dropCount := msgCount - pruneToMessages
	pm.session.messages = pm.session.messages[dropCount:]

	return dropCount
}
