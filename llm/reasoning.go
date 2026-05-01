package llm

import "github.com/openai/openai-go/shared"

// Abstract reasoning levels. Mirror config.ReasoningLow/Medium/High to keep
// llm self-contained (no dependency on the config package).
const (
	ReasoningLow    = "low"
	ReasoningMedium = "medium"
	ReasoningHigh   = "high"
)

// openAIReasoningEffort maps an abstract reasoning level to the OpenAI SDK's
// ReasoningEffort enum. Returns "" for unset/unknown levels so callers can omit
// the field. Used for both OpenAI Chat Completions and Ollama's OpenAI-compat
// endpoint (which forwards reasoning_effort to its internal Think field).
func openAIReasoningEffort(level string) shared.ReasoningEffort {
	switch level {
	case ReasoningLow:
		return shared.ReasoningEffortLow
	case ReasoningMedium:
		return shared.ReasoningEffortMedium
	case ReasoningHigh:
		return shared.ReasoningEffortHigh
	}
	return ""
}

// anthropicBudgetTokens maps an abstract reasoning level to a token budget for
// Anthropic's extended thinking config.
func anthropicBudgetTokens(level string) int64 {
	switch level {
	case ReasoningLow:
		return 2048
	case ReasoningMedium:
		return 8192
	case ReasoningHigh:
		return 24576
	}
	return 0
}

// geminiBudgetTokens maps an abstract reasoning level to a token budget for
// Gemini's ThinkingConfig. Returns int32 to match the SDK field type.
func geminiBudgetTokens(level string) int32 {
	switch level {
	case ReasoningLow:
		return 2048
	case ReasoningMedium:
		return 8192
	case ReasoningHigh:
		return 24576
	}
	return 0
}
