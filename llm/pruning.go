package llm

import (
	"fmt"
	"sync"
)

// PruningManager tracks tool results and prunes old messages based on
// configured defaults and optional LLM-specified overrides.
type PruningManager struct {
	session                    *Session
	mu                         sync.Mutex
	messageCount               int              // Total messages tracked
	toolHistory                map[string][]int  // toolName -> ordered list of message indices
	trackedMsgs                map[int]string    // messageIndex -> toolName (reverse lookup)
	defaultToolRecencyLimit    int               // Agent-level default (0 = disabled)
	defaultMessageRecencyLimit int               // Agent-level default (0 = disabled)
}

// NewPruningManager creates a new pruning manager for the given session.
// Default limits are applied to every tool result unless overridden by the LLM.
func NewPruningManager(session *Session, defaultToolRecencyLimit, defaultMessageRecencyLimit int) *PruningManager {
	return &PruningManager{
		session:                    session,
		toolHistory:                make(map[string][]int),
		trackedMsgs:                make(map[int]string),
		defaultToolRecencyLimit:    defaultToolRecencyLimit,
		defaultMessageRecencyLimit: defaultMessageRecencyLimit,
	}
}

// RegisterAndPrune registers a new tool result and applies pruning.
// Call this after a tool result observation has been added to the session.
//
// The effective limits are resolved as:
//   - If overrideToolRecency > 0, use it; otherwise use the default
//   - If overrideMsgRecency > 0, use it; otherwise use the default
//
// Returns the number of messages pruned.
func (pm *PruningManager) RegisterAndPrune(toolName string, overrideToolRecency, overrideMsgRecency int) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// The observation is the user message just added (second-to-last in session,
	// since SendStream appends [user, assistant]). But actually, the user message
	// containing the observation was appended as the second-to-last message.
	// We need the index of the user message (the observation), not the assistant response.
	msgCount := len(pm.session.messages)
	if msgCount < 2 {
		return 0
	}

	// The observation is at index msgCount-2 (user msg), response is at msgCount-1 (assistant)
	msgIndex := msgCount - 2

	// Track this message
	pm.messageCount++
	pm.toolHistory[toolName] = append(pm.toolHistory[toolName], msgIndex)
	pm.trackedMsgs[msgIndex] = toolName

	// Set metadata on the observation message
	pm.session.messages[msgIndex].Metadata = &MessageMetadata{
		MessageID:    fmt.Sprintf("msg_%d", pm.messageCount),
		ToolName:     toolName,
		MessageIndex: msgIndex,
		IsPrunable:   true,
	}

	// Resolve effective limits: LLM override > default
	toolRecencyLimit := pm.defaultToolRecencyLimit
	if overrideToolRecency > 0 {
		toolRecencyLimit = overrideToolRecency
	}

	messageRecencyLimit := pm.defaultMessageRecencyLimit
	if overrideMsgRecency > 0 {
		messageRecencyLimit = overrideMsgRecency
	}

	prunedCount := 0

	// Apply tool recency pruning
	if toolRecencyLimit > 0 {
		prunedCount += pm.pruneByToolRecency(toolName, toolRecencyLimit)
	}

	// Apply message recency pruning
	if messageRecencyLimit > 0 {
		prunedCount += pm.pruneByMessageRecency(messageRecencyLimit)
	}

	return prunedCount
}

// pruneByToolRecency keeps only the last N results from the specified tool
func (pm *PruningManager) pruneByToolRecency(toolName string, limit int) int {
	history := pm.toolHistory[toolName]
	if len(history) <= limit {
		return 0
	}

	prunedCount := 0
	excess := len(history) - limit

	for i := 0; i < excess; i++ {
		msgIdx := history[i]
		if pm.pruneMessage(msgIdx) {
			prunedCount++
		}
	}

	// Update history to only keep the recent ones
	pm.toolHistory[toolName] = history[excess:]

	return prunedCount
}

// pruneByMessageRecency prunes tool results older than N messages ago
func (pm *PruningManager) pruneByMessageRecency(limit int) int {
	currentIdx := len(pm.session.messages) - 1
	threshold := currentIdx - limit

	prunedCount := 0

	for msgIdx, toolName := range pm.trackedMsgs {
		if msgIdx < threshold {
			if pm.pruneMessage(msgIdx) {
				prunedCount++
				// Remove from tool history
				pm.removeFromToolHistory(toolName, msgIdx)
			}
		}
	}

	return prunedCount
}

// pruneMessage replaces a message's content with the pruned marker
// Returns true if the message was pruned, false if already pruned
func (pm *PruningManager) pruneMessage(msgIdx int) bool {
	if msgIdx < 0 || msgIdx >= len(pm.session.messages) {
		return false
	}

	msg := &pm.session.messages[msgIdx]

	// Skip if already pruned
	if msg.Content == "[RESULT PRUNED]" {
		return false
	}

	// Replace content with marker
	msg.Content = "[RESULT PRUNED]"
	msg.Parts = nil // Clear any multimodal content

	// Remove from tracking
	delete(pm.trackedMsgs, msgIdx)

	return true
}

// removeFromToolHistory removes a message index from a tool's history
func (pm *PruningManager) removeFromToolHistory(toolName string, msgIdx int) {
	history := pm.toolHistory[toolName]
	for i, idx := range history {
		if idx == msgIdx {
			pm.toolHistory[toolName] = append(history[:i], history[i+1:]...)
			return
		}
	}
}

// GetTrackedCount returns the number of currently tracked (non-pruned) tool results
func (pm *PruningManager) GetTrackedCount() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return len(pm.trackedMsgs)
}

// GetToolResultCount returns the number of tracked results for a specific tool
func (pm *PruningManager) GetToolResultCount(toolName string) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return len(pm.toolHistory[toolName])
}
