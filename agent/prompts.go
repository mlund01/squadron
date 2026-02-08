package agent

import (
	"squadron/agent/internal/prompts"
)

// AgentInfo represents basic info about an agent for the supervisor prompt
type AgentInfo = prompts.AgentInfo

// IterationOptions contains info about task iteration for conditional prompt content
type IterationOptions = prompts.IterationOptions

// GetSupervisorPrompt returns the supervisor system prompt with available agents injected
// This is a public wrapper for the internal prompts package
func GetSupervisorPrompt(agents []AgentInfo, iterOpts IterationOptions) string {
	return prompts.GetSupervisorPrompt(agents, iterOpts)
}
