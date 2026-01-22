package agent

import (
	"squad/agent/internal/prompts"
)

// AgentInfo represents basic info about an agent for the supervisor prompt
type AgentInfo = prompts.AgentInfo

// GetSupervisorPrompt returns the supervisor system prompt with available agents injected
// This is a public wrapper for the internal prompts package
func GetSupervisorPrompt(agents []AgentInfo) string {
	return prompts.GetSupervisorPrompt(agents)
}
