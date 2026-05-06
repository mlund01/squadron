package agent

import (
	"context"

	"squadron/llm"
)

// Tool-call interruption messages. Each one is matter-of-fact so the LLM
// has the facts and decides how to proceed (retry vs verify vs skip).
const (
	// InterruptedToolMessage — the tool was actively running when the
	// system was shut down. Side effects may or may not have completed
	// externally; the result was not received.
	InterruptedToolMessage = "Tool call was interrupted by a system shutdown before it completed."

	// QueuedToolMessage — the tool was queued in the same assistant turn
	// as another tool, but the system shut down before this one started.
	// No side effects; safe to retry.
	QueuedToolMessage = "Tool call was queued but the system shut down before it started."
)

// MaybeInterrupted substitutes InterruptedToolMessage for the original
// result when the context has been canceled. Used at every tool.Call
// site so the persisted tool_result is comprehensible on resume.
func MaybeInterrupted(ctx context.Context, result string) string {
	if ctx.Err() != nil {
		return InterruptedToolMessage
	}
	return result
}

// HealSessionMessages fixes stored session messages for an interrupted agent.
// If the last message is assistant with tool_use blocks, the tool call was in-flight
// and the result was never stored. Inject placeholder ToolResultBlocks so the agent
// LLM can decide whether to re-run the tools.
// If the last message is user, the LLM was interrupted mid-response — leave as-is
// (ContinueStream will pick up from there).
func HealSessionMessages(msgs []llm.Message) []llm.Message {
	if len(msgs) == 0 {
		return msgs
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant {
		return msgs
	}

	// Check for tool_use blocks in Parts
	var toolUseIDs []string
	for _, part := range last.Parts {
		if part.Type == llm.ContentTypeToolUse && part.ToolUse != nil {
			toolUseIDs = append(toolUseIDs, part.ToolUse.ID)
		}
	}

	if len(toolUseIDs) == 0 {
		return msgs
	}

	// Inject placeholder tool results for each interrupted tool call
	var resultParts []llm.ContentBlock
	for _, id := range toolUseIDs {
		resultParts = append(resultParts, llm.ContentBlock{
			Type: llm.ContentTypeToolResult,
			ToolResult: &llm.ToolResultBlock{
				ToolUseID: id,
				Content:   InterruptedToolMessage,
				IsError:   true,
			},
		})
	}

	return append(msgs, llm.Message{
		Role:  llm.RoleUser,
		Parts: resultParts,
	})
}
