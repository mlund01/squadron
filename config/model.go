package config

import "fmt"

type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderGemini    Provider = "gemini"
	ProviderAnthropic Provider = "anthropic"
	ProviderOllama    Provider = "ollama"
)

// ModelInfo describes a single registered model: the wire-name sent to the
// provider and the capability flags Squadron needs to know about.
//
// To register a new model: add an entry to SupportedModels (keyed by the
// HCL-friendly identifier the user references, e.g. `models.openai.gpt_5`)
// with the API name and any capability flags. New capability flags should
// be added as additional fields here, not as side tables.
type ModelInfo struct {
	// APIName is the wire identifier sent in the provider's request body.
	APIName string

	// Reasoning is true when the model supports native provider-side
	// reasoning (Anthropic extended thinking, OpenAI Responses reasoning,
	// Gemini ThinkingConfig). Setting `reasoning = "..."` on an agent
	// whose resolved model has this false logs a warning at startup and
	// the session runs without reasoning.
	Reasoning bool
}

// SupportedModels is the registry of every model Squadron ships built-in
// support for. Provider → HCL-friendly key → ModelInfo.
//
// HCL refs (`models.openai.gpt_5`) resolve through this table. Ollama keeps
// an empty map because users register their own models via `aliases`.
var SupportedModels = map[Provider]map[string]ModelInfo{
	ProviderOpenAI: {
		// Reasoning models — gpt-5 family, o-series 3 and 4. o1 is
		// excluded because the API rejects reasoning_effort on it.
		"gpt_5_5":       {APIName: "gpt-5.5", Reasoning: true},
		"gpt_5_5_pro":   {APIName: "gpt-5.5-pro", Reasoning: true},
		"gpt_5_4":       {APIName: "gpt-5.4", Reasoning: true},
		"gpt_5_4_mini":  {APIName: "gpt-5.4-mini", Reasoning: true},
		"gpt_5_4_nano":  {APIName: "gpt-5.4-nano", Reasoning: true},
		"gpt_5_4_pro":   {APIName: "gpt-5.4-pro", Reasoning: true},
		"gpt_5_3_codex": {APIName: "gpt-5.3-codex", Reasoning: true},
		"gpt_5_2":       {APIName: "gpt-5.2", Reasoning: true},
		"gpt_5":         {APIName: "gpt-5", Reasoning: true},
		"gpt_5_mini":    {APIName: "gpt-5-mini", Reasoning: true},
		"gpt_5_nano":    {APIName: "gpt-5-nano", Reasoning: true},
		// o3, o4-mini, o3-mini are deprecated (API shutdown 2026-10-23) but
		// still functional today; keep them registered until shutdown.
		"o3":      {APIName: "o3", Reasoning: true},
		"o4_mini": {APIName: "o4-mini", Reasoning: true},
		"o3_mini": {APIName: "o3-mini", Reasoning: true},

		// Non-reasoning chat models.
		"gpt_4_1":      {APIName: "gpt-4.1"},
		"gpt_4_1_mini": {APIName: "gpt-4.1-mini"},
		"gpt_4_1_nano": {APIName: "gpt-4.1-nano"},
		"gpt_4o":       {APIName: "gpt-4o"},
		"gpt_4o_mini":  {APIName: "gpt-4o-mini"},
		// gpt-4-turbo and o1 are deprecated (shutdown 2026-10-23). o1-mini
		// is fully retired (shutdown 2025-10-27) and removed from the
		// registry — requests to it return 404 now.
		"gpt_4_turbo": {APIName: "gpt-4-turbo"},
		"o1":          {APIName: "o1"},
	},
	ProviderGemini: {
		// Gemini 2.5+ and 3.x support thinking.
		"gemini_3_1_pro_preview":        {APIName: "gemini-3.1-pro-preview", Reasoning: true},
		"gemini_3_1_flash_lite_preview": {APIName: "gemini-3.1-flash-lite-preview", Reasoning: true},
		"gemini_3_flash_preview":        {APIName: "gemini-3-flash-preview", Reasoning: true},
		"gemini_2_5_pro":                {APIName: "gemini-2.5-pro", Reasoning: true},
		"gemini_2_5_flash":              {APIName: "gemini-2.5-flash", Reasoning: true},
		"gemini_2_5_flash_lite":         {APIName: "gemini-2.5-flash-lite", Reasoning: true},

		// Earlier Gemini families don't support thinking. Gemini 2.0 flash
		// variants are deprecated with a 2026-06-01 shutdown — keep them
		// registered until then. The entire 1.5 family was retired in
		// September 2025 (API returns 404) and is no longer registered.
		"gemini_2_0_flash":      {APIName: "gemini-2.0-flash"},
		"gemini_2_0_flash_lite": {APIName: "gemini-2.0-flash-lite"},
		"gemini_2_0_flash_exp":  {APIName: "gemini-2.0-flash-exp"},
	},
	ProviderAnthropic: {
		// Claude 4.x family supports extended thinking. claude-opus-4 and
		// claude-sonnet-4 (the original 2025-05-14 SKUs) are deprecated as
		// of 2026-04-14 with shutdown on 2026-06-15 — kept registered until
		// then so existing missions don't break.
		"claude_opus_4_7":   {APIName: "claude-opus-4-7", Reasoning: true},
		"claude_opus_4_6":   {APIName: "claude-opus-4-6", Reasoning: true},
		"claude_opus_4_5":   {APIName: "claude-opus-4-5-20251101", Reasoning: true},
		"claude_sonnet_4_6": {APIName: "claude-sonnet-4-6", Reasoning: true},
		"claude_sonnet_4_5": {APIName: "claude-sonnet-4-5-20250929", Reasoning: true},
		"claude_sonnet_4":   {APIName: "claude-sonnet-4-20250514", Reasoning: true},
		"claude_opus_4":     {APIName: "claude-opus-4-20250514", Reasoning: true},
		"claude_haiku_4_5":  {APIName: "claude-haiku-4-5-20251001", Reasoning: true},
		// Claude 3.5 Sonnet was retired 2025-10-28; Claude 3.5 Haiku was
		// retired 2026-02-19. Both removed from the registry.
	},
	// Ollama models are user-registered via `aliases` on the model block.
	// Capability flags can't be inferred and aren't currently surfaced —
	// `reasoning = "..."` on an Ollama agent is a no-op + warning.
	ProviderOllama: {},
}

// BuildPricingOverrides builds a map of API model name → pricing from all
// model configs. Only includes models that have explicit pricing blocks.
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
	Name          string                         `hcl:"name,label"`
	Provider      Provider                       `hcl:"provider"`
	Aliases       map[string]string              `hcl:"-"` // HCL key → API model name (parsed manually)
	APIKey        string                         `hcl:"api_key,optional"`
	BaseURL       string                         `hcl:"base_url,optional"`
	PromptCaching *bool                          `hcl:"prompt_caching,optional"`
	Pricing       map[string]*ModelPricingConfig `json:"-"` // model name → pricing override
}

// AvailableModels returns all HCL keys available for this provider mapped to
// their API name. Combines built-in SupportedModels entries with any user
// Aliases (the Aliases map wins on conflict).
func (m *Model) AvailableModels() map[string]string {
	result := make(map[string]string)
	if supported, ok := SupportedModels[m.Provider]; ok {
		for key, info := range supported {
			result[key] = info.APIName
		}
	}
	for key, apiName := range m.Aliases {
		result[key] = apiName
	}
	return result
}

// ModelInfoByAPIName looks up the registered ModelInfo for a given API name
// on this model's provider. Returns ok=false if the API name isn't registered
// (e.g. a user-aliased Ollama model). Linear scan; the registry is small and
// this is only called at agent construction.
func (m *Model) ModelInfoByAPIName(apiName string) (ModelInfo, bool) {
	if supported, ok := SupportedModels[m.Provider]; ok {
		for _, info := range supported {
			if info.APIName == apiName {
				return info, true
			}
		}
	}
	return ModelInfo{}, false
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

	if m.Provider == ProviderOllama {
		if m.BaseURL == "" {
			return fmt.Errorf("base_url is required for provider '%s'", m.Provider)
		}
		if len(m.Aliases) == 0 {
			return fmt.Errorf("aliases are required for provider '%s' — define model mappings like: aliases = { gemma4 = \"gemma4\" }", m.Provider)
		}
		return nil
	}

	if m.APIKey == "" {
		return fmt.Errorf("api_key is required for provider '%s'", m.Provider)
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
