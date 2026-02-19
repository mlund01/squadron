package agent

import (
	"strings"

	"squadron/llm"
)

// HealSessionMessages fixes stored session messages for an interrupted agent.
// If the last message is assistant with ACTION, the tool call was in-flight and the
// observation was never stored. Inject a placeholder so the agent LLM can decide
// whether to re-run the tool.
// If the last message is user, the LLM was interrupted mid-response — leave as-is
// (ContinueStream will pick up from there).
func HealSessionMessages(msgs []llm.Message) []llm.Message {
	if len(msgs) == 0 {
		return msgs
	}
	last := msgs[len(msgs)-1]
	if last.Role == llm.RoleAssistant && strings.Contains(last.Content, "<ACTION>") {
		return append(msgs, llm.Message{
			Role:    llm.RoleUser,
			Content: "<OBSERVATION>\nObservation unavailable — the tool call was interrupted by a system restart. You may need to re-run the tool or verify its result.\n</OBSERVATION>",
		})
	}
	return msgs
}
