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
		"gpt_5_5":          "gpt-5.5",
		"gpt_5_5_pro":      "gpt-5.5-pro",
		"gpt_5_4":          "gpt-5.4",
		"gpt_5_4_mini":     "gpt-5.4-mini",
		"gpt_5_4_nano":     "gpt-5.4-nano",
		"gpt_5_4_pro":      "gpt-5.4-pro",
		"gpt_5_3_codex":    "gpt-5.3-codex",
		"gpt_5_2":          "gpt-5.2",
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
		"gemini_3_1_pro_preview":        "gemini-3.1-pro-preview",
		"gemini_3_1_flash_lite_preview": "gemini-3.1-flash-lite-preview",
		"gemini_3_flash_preview":        "gemini-3-flash-preview",
		"gemini_2_5_pro":                "gemini-2.5-pro",
		"gemini_2_5_flash":              "gemini-2.5-flash",
		"gemini_2_5_flash_lite":         "gemini-2.5-flash-lite",
		"gemini_2_0_flash":              "gemini-2.0-flash",
		"gemini_2_0_flash_lite":         "gemini-2.0-flash-lite",
		"gemini_2_0_flash_exp":          "gemini-2.0-flash-exp",
		"gemini_1_5_pro":                "gemini-1.5-pro",
		"gemini_1_5_flash":              "gemini-1.5-flash",
	},
	ProviderAnthropic: {
		"claude_opus_4_7":   "claude-opus-4-7",
		"claude_opus_4_6":   "claude-opus-4-6",
		"claude_opus_4_5":   "claude-opus-4-5-20251101",
		"claude_sonnet_4_6": "claude-sonnet-4-6",
		"claude_sonnet_4_5": "claude-sonnet-4-5-20250929",
		"claude_sonnet_4":   "claude-sonnet-4-20250514",
		"claude_opus_4":     "claude-opus-4-20250514",
		"claude_haiku_4_5":  "claude-haiku-4-5-20251001",
		"claude_3_5_haiku":  "claude-3-5-haiku-20241022",
		"claude_3_5_sonnet": "claude-3-5-sonnet-20241022",
	},
	ProviderOllama: {},
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
		available := m.AvailableModels()
		for hclName, pc := range m.Pricing {
			if apiName, ok := available[hclName]; ok {
				overrides[apiName] = pc
			} else {
				overrides[hclName] = pc
			}
		}
	}
	return overrides
}

type Model struct {
	Name           string            `hcl:"name,label"`
	Provider       Provider          `hcl:"provider"`
	Aliases        map[string]string `hcl:"-"` // HCL key → API model name (parsed manually)
	APIKey         string            `hcl:"api_key,optional"`
	BaseURL        string            `hcl:"base_url,optional"`
	PromptCaching  *bool             `hcl:"prompt_caching,optional"`
	Pricing        map[string]*ModelPricingConfig `json:"-"` // model name → pricing override
	// ReasoningModels lists alias keys (or API model names) that should be
	// treated as reasoning-capable, even if the built-in detector doesn't
	// know about them. Required for Ollama (where users register their own
	// aliases) and useful for newly released cloud models or custom proxies.
	ReasoningModels []string `hcl:"-"` // parsed manually
}

// AvailableModels returns all model keys available for this provider.
// Combines: all SupportedModels for this provider + Aliases.
func (m *Model) AvailableModels() map[string]string {
	result := make(map[string]string)

	// Add all internal mappings for this provider
	if supported, ok := SupportedModels[m.Provider]; ok {
		for key, apiName := range supported {
			result[key] = apiName
		}
	}

	// Aliases override/extend
	for key, apiName := range m.Aliases {
		result[key] = apiName
	}

	return result
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
		return fmt.Errorf("unsupported provider '%s'", m.Provider)
	}

	// Ollama (and other local providers) require base_url instead of api_key
	if m.Provider == ProviderOllama {
		if m.BaseURL == "" {
			return fmt.Errorf("base_url is required for provider '%s'", m.Provider)
		}
		if len(m.Aliases) == 0 {
			return fmt.Errorf("aliases are required for provider '%s' — define model mappings like: aliases = { gemma4 = \"gemma4\" }", m.Provider)
		}
	} else {
		// Cloud providers require an API key
		if m.APIKey == "" {
			return fmt.Errorf("api_key is required for provider '%s'", m.Provider)
		}
	}

	// Validate that each reasoning_models entry resolves to a known model
	// (alias key or built-in API name).
	if len(m.ReasoningModels) > 0 {
		available := m.AvailableModels()
		for _, entry := range m.ReasoningModels {
			if _, ok := available[entry]; ok {
				continue
			}
			// Also accept raw API names (e.g. "deepseek-r1" instead of the alias key)
			matched := false
			for _, apiName := range available {
				if apiName == entry {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("reasoning_models entry %q: no such model on provider %q", entry, m.Provider)
			}
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
