package config

import "fmt"

type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderGemini    Provider = "gemini"
	ProviderAnthropic Provider = "anthropic"
	ProviderOllama    Provider = "ollama"
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
		"gemini_2_5_pro":       "gemini-2.5-pro",
		"gemini_2_5_flash":     "gemini-2.5-flash",
		"gemini_2_5_flash_lite": "gemini-2.5-flash-lite",
		"gemini_2_0_flash":     "gemini-2.0-flash",
		"gemini_2_0_flash_exp": "gemini-2.0-flash-exp",
		"gemini_1_5_pro":       "gemini-1.5-pro",
		"gemini_1_5_flash":     "gemini-1.5-flash",
	},
	ProviderAnthropic: {
		"claude_opus_4_6":   "claude-opus-4-6",
		"claude_sonnet_4_6": "claude-sonnet-4-6",
		"claude_sonnet_4":   "claude-sonnet-4-20250514",
		"claude_opus_4":     "claude-opus-4-20250514",
		"claude_haiku_4_5":  "claude-haiku-4-5-20251001",
		"claude_3_5_haiku":  "claude-3-5-haiku-20241022",
		"claude_3_5_sonnet": "claude-3-5-sonnet-20241022",
	},
	ProviderOllama: {
		"gemma_4": "gemma4",
	},
}

// Model represents a model provider configuration
// BuildPricingOverrides builds a map of API model name → pricing from all model configs.
// Only includes models that have explicit pricing blocks.
func BuildPricingOverrides(models []Model) map[string]*ModelPricingConfig {
	overrides := make(map[string]*ModelPricingConfig)
	for _, m := range models {
		if m.Pricing == nil {
			continue
		}
		supported := SupportedModels[m.Provider]
		for hclName, pc := range m.Pricing {
			// Resolve HCL model name to API model name
			if apiName, ok := supported[hclName]; ok {
				overrides[apiName] = pc
			} else {
				// User might have used the API name directly
				overrides[hclName] = pc
			}
		}
	}
	return overrides
}

type Model struct {
	Name           string            `hcl:"name,label"`
	Provider       Provider          `hcl:"provider"`
	AllowedModels  []string          `hcl:"allowed_models"`
	APIKey         string            `hcl:"api_key,optional"`
	BaseURL        string            `hcl:"base_url,optional"`
	PromptCaching  *bool             `hcl:"prompt_caching,optional"`
	Pricing        map[string]*ModelPricingConfig `json:"-"` // model name → pricing override
}

// ModelPricingConfig holds per-million-token cost overrides for a model.
type ModelPricingConfig struct {
	Input      float64 `hcl:"input"`
	Output     float64 `hcl:"output"`
	CacheRead  float64 `hcl:"cache_read,optional"`
	CacheWrite float64 `hcl:"cache_write,optional"`
}

// IsPromptCachingEnabled returns whether prompt caching is enabled (defaults to true).
func (m *Model) IsPromptCachingEnabled() bool {
	if m.PromptCaching == nil {
		return true
	}
	return *m.PromptCaching
}

func (m *Model) Validate() error {
	if _, ok := SupportedModels[m.Provider]; !ok {
		return fmt.Errorf("Unsupported provider; Provider '%s' is not supported", m.Provider)
	}

	// Ollama (and other local providers) allow arbitrary model names since users
	// pull whatever models they want. Require base_url instead of api_key.
	if m.Provider == ProviderOllama {
		if m.BaseURL == "" {
			return fmt.Errorf("base_url is required for provider '%s'", m.Provider)
		}
		return nil
	}

	// Cloud providers require an API key
	if m.APIKey == "" {
		return fmt.Errorf("api_key is required for provider '%s'", m.Provider)
	}

	supportedForProvider := SupportedModels[m.Provider]
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
