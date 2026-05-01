package config

import (
	"fmt"
	"strings"
)

const (
	ReasoningLow    = "low"
	ReasoningMedium = "medium"
	ReasoningHigh   = "high"
)

// NormalizeReasoning lowercases and validates a reasoning level. Returns
// ("", nil) for empty input. Errors on unknown values.
func NormalizeReasoning(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case ReasoningLow, ReasoningMedium, ReasoningHigh:
		return v, nil
	default:
		return "", fmt.Errorf("invalid reasoning value %q: must be one of %q, %q, %q", s, ReasoningLow, ReasoningMedium, ReasoningHigh)
	}
}

// ModelSupportsReasoning returns true if the given API model name has native
// reasoning on the model's provider. Detection is prefix-based on stable
// cloud-provider naming (Claude 4.x, o3/o4/gpt-5, Gemini 2.5+/3.x). Models
// hosted by local servers (Ollama and OpenAI-compatible proxies) return
// false — reasoning on those is opt-out via no-op rather than opt-in via
// declaration, matching the conventional "set the flag, model decides"
// pattern.
func ModelSupportsReasoning(m *Model, apiName string) bool {
	if m == nil {
		return false
	}
	return builtinReasoningSupport(m.Provider, apiName)
}

func builtinReasoningSupport(provider Provider, apiName string) bool {
	m := strings.ToLower(apiName)
	switch provider {
	case ProviderAnthropic:
		switch {
		case strings.HasPrefix(m, "claude-opus-4"),
			strings.HasPrefix(m, "claude-sonnet-4"),
			strings.HasPrefix(m, "claude-haiku-4"):
			return true
		}
	case ProviderOpenAI:
		switch {
		case strings.HasPrefix(m, "o3"),
			strings.HasPrefix(m, "o4"),
			strings.HasPrefix(m, "gpt-5"):
			return true
		}
	case ProviderGemini:
		switch {
		case strings.HasPrefix(m, "gemini-2.5"),
			strings.HasPrefix(m, "gemini-3"):
			return true
		}
	case ProviderOllama:
		return false
	}
	return false
}
