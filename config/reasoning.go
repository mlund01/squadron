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

// ModelSupportsReasoning returns true if the API model name resolves to a
// registered ModelInfo with Reasoning=true on the model's provider. Models
// that aren't in SupportedModels (notably user-aliased Ollama models) return
// false — capability is opt-in via registry entry, not by inference.
func ModelSupportsReasoning(m *Model, apiName string) bool {
	if m == nil {
		return false
	}
	info, ok := m.ModelInfoByAPIName(apiName)
	if !ok {
		return false
	}
	return info.Reasoning
}
