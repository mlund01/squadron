package agent

import (
	"squadron/llm"
)

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
				Content:   "Tool call was interrupted by a system restart. You may need to re-run the tool or verify its result.",
				IsError:   true,
			},
		})
	}

	return append(msgs, llm.Message{
		Role:  llm.RoleUser,
		Parts: resultParts,
	})
}
