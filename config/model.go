package config

import "fmt"

type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderGemini    Provider = "gemini"
	ProviderAnthropic Provider = "anthropic"
)

// SupportedModels maps provider to their supported model names
// The keys are the variable names used in HCL references (e.g., models.openai.gpt_4o)
var SupportedModels = map[Provider]map[string]string{
	ProviderOpenAI: {
		"gpt_5":            "gpt-5",
		"gpt_5_mini":       "gpt-5-mini",
		"gpt_5_nano":       "gpt-5-nano",
		"gpt_4_1":      "gpt-4.1",
		"gpt_4_1_mini": "gpt-4.1-mini",
		"gpt_4_1_nano": "gpt-4.1-nano",
		"o3":           "o3",
		"o4_mini":      "o4-mini",
		"gpt_4o":       "gpt-4o",
		"gpt_4o_mini":  "gpt-4o-mini",
		"gpt_4_turbo":  "gpt-4-turbo",
		"o1":           "o1",
		"o1_mini":      "o1-mini",
		"o3_mini":      "o3-mini",
	},
	ProviderGemini: {
		"gemini_2_0_flash":     "gemini-2.0-flash",
		"gemini_1_5_pro":       "gemini-1.5-pro",
		"gemini_1_5_flash":     "gemini-1.5-flash",
		"gemini_2_0_flash_exp": "gemini-2.0-flash-exp",
	},
	ProviderAnthropic: {
		"claude_sonnet_4":   "claude-sonnet-4-20250514",
		"claude_opus_4":     "claude-opus-4-20250514",
		"claude_3_5_haiku":  "claude-3-5-haiku-20241022",
		"claude_3_5_sonnet": "claude-3-5-sonnet-20241022",
	},
}

// Model represents a model provider configuration
type Model struct {
	Name          string   `hcl:"name,label"`
	Provider      Provider `hcl:"provider"`
	AllowedModels []string `hcl:"allowed_models"`
	APIKey        string   `hcl:"api_key"`
}

func (m *Model) Validate() error {
	supportedForProvider, ok := SupportedModels[m.Provider]
	if !ok {
		return fmt.Errorf("Unsupported provider; Provider '%s' is not supported", m.Provider)
	}

	for _, modelName := range m.AllowedModels {
		found := false
		for varName := range supportedForProvider {
			if varName == modelName {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Unsupported model; Model '%s' is not supported for provider '%s'. Supported models: %v", modelName, m.Provider, getKeys(supportedForProvider))
		}
	}
	return nil
}

func getKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
