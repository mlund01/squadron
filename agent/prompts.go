package agent

import (
	"squadron/agent/internal/prompts"
)

// AgentInfo represents basic info about an agent for the commander prompt
type AgentInfo = prompts.AgentInfo

// IterationOptions contains info about task iteration for conditional prompt content
type IterationOptions = prompts.IterationOptions

// GetCommanderPrompt returns the commander system prompt with available agents injected
// This is a public wrapper for the internal prompts package
func GetCommanderPrompt(agents []AgentInfo, iterOpts IterationOptions) string {
	return prompts.GetCommanderPrompt(agents, iterOpts)
}
