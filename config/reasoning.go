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
// reasoning on the model's provider. The check considers both the user-declared
// reasoning_models override and the built-in prefix detector. The override is
// additive: it can force-enable a model the built-in list doesn't know about,
// but cannot force-disable a model the built-in list already recognizes.
func ModelSupportsReasoning(m *Model, apiName string) bool {
	if m == nil {
		return false
	}

	if len(m.ReasoningModels) > 0 {
		// reasoning_models entries may be either an alias key (HCL-side name)
		// or an API model name. Resolve aliases up front so both forms match.
		available := m.AvailableModels()
		for _, entry := range m.ReasoningModels {
			if entry == apiName {
				return true
			}
			if mapped, ok := available[entry]; ok && mapped == apiName {
				return true
			}
		}
	}

	return builtinReasoningSupport(m.Provider, apiName)
}

// builtinReasoningSupport returns true for cloud-managed models with stable
// naming. Provider-specific prefix matching only — local providers like Ollama
// always return false here and rely on the reasoning_models override.
func builtinReasoningSupport(provider Provider, apiName string) bool {
	m := strings.ToLower(apiName)
	switch provider {
	case ProviderAnthropic:
		// Claude 4.x family supports extended thinking.
		// Excludes claude-3-* and earlier.
		switch {
		case strings.HasPrefix(m, "claude-opus-4"),
			strings.HasPrefix(m, "claude-sonnet-4"),
			strings.HasPrefix(m, "claude-haiku-4"):
			return true
		}
	case ProviderOpenAI:
		// o3, o4, gpt-5 family all accept reasoning_effort.
		// Excludes o1*, gpt-4*, gpt-3* — o1 historically rejects the param.
		switch {
		case strings.HasPrefix(m, "o3"),
			strings.HasPrefix(m, "o4"),
			strings.HasPrefix(m, "gpt-5"):
			return true
		}
	case ProviderGemini:
		// Gemini 2.5 and 3.x support thinking_config.
		// Excludes gemini-1.5*, gemini-2.0*.
		switch {
		case strings.HasPrefix(m, "gemini-2.5"),
			strings.HasPrefix(m, "gemini-3"):
			return true
		}
	case ProviderOllama:
		// Ollama hosts user-named models; no stable prefix to match.
		// Users declare reasoning support via reasoning_models on the model block.
		return false
	}
	return false
}

