package config

import "testing"

func TestNormalizeReasoning(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"low", "low", false},
		{"medium", "medium", false},
		{"high", "high", false},
		{"LOW", "low", false},
		{"  High  ", "high", false},
		{"extreme", "", true},
		{"none", "", true},
		{"off", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeReasoning(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("NormalizeReasoning(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeReasoning(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestModelSupportsReasoning_FromRegistry exercises the registry-backed
// capability lookup. Adding a new model to SupportedModels with
// Reasoning: true is the only step needed for it to be picked up — there's
// no separate prefix list to keep in sync.
func TestModelSupportsReasoning_FromRegistry(t *testing.T) {
	cases := []struct {
		provider Provider
		apiName  string
		want     bool
	}{
		// Anthropic — Claude 4 family is reasoning-capable, 3.x is not.
		{ProviderAnthropic, "claude-opus-4-7", true},
		{ProviderAnthropic, "claude-sonnet-4-6", true},
		{ProviderAnthropic, "claude-haiku-4-5-20251001", true},
		{ProviderAnthropic, "claude-3-5-sonnet-20241022", false},
		{ProviderAnthropic, "claude-3-5-haiku-20241022", false},
		// OpenAI — o3/o4/gpt-5 family flagged; o1 and gpt-4* are not.
		{ProviderOpenAI, "o3", true},
		{ProviderOpenAI, "o3-mini", true},
		{ProviderOpenAI, "o4-mini", true},
		{ProviderOpenAI, "gpt-5", true},
		{ProviderOpenAI, "gpt-5.5-pro", true},
		{ProviderOpenAI, "o1", false},
		{ProviderOpenAI, "o1-mini", false},
		{ProviderOpenAI, "gpt-4o", false},
		{ProviderOpenAI, "gpt-4.1", false},
		// Gemini — 2.5+/3.x flagged; 2.0 and 1.5 are not.
		{ProviderGemini, "gemini-2.5-pro", true},
		{ProviderGemini, "gemini-2.5-flash", true},
		{ProviderGemini, "gemini-3.1-pro-preview", true},
		{ProviderGemini, "gemini-2.0-flash", false},
		{ProviderGemini, "gemini-1.5-pro", false},
		// Ollama — empty registry, every lookup is false.
		{ProviderOllama, "deepseek-r1", false},
		{ProviderOllama, "qwen3", false},
		// Unknown API name on a known provider — false (not registered).
		{ProviderAnthropic, "claude-7-imaginary", false},
	}
	for _, tc := range cases {
		m := &Model{Provider: tc.provider}
		if got := ModelSupportsReasoning(m, tc.apiName); got != tc.want {
			t.Errorf("ModelSupportsReasoning(%v, %q) = %v, want %v", tc.provider, tc.apiName, got, tc.want)
		}
	}
}

// TestModelInfoByAPIName_RegistryConsistency catches drift between the
// SupportedModels keys and their registered API names — every entry must be
// findable via reverse-lookup, since that's how capability checks resolve.
func TestModelInfoByAPIName_RegistryConsistency(t *testing.T) {
	for provider, models := range SupportedModels {
		m := &Model{Provider: provider}
		for hclKey, info := range models {
			got, ok := m.ModelInfoByAPIName(info.APIName)
			if !ok {
				t.Errorf("%s/%s: API name %q not findable via ModelInfoByAPIName", provider, hclKey, info.APIName)
				continue
			}
			if got.APIName != info.APIName {
				t.Errorf("%s/%s: reverse lookup APIName = %q, want %q", provider, hclKey, got.APIName, info.APIName)
			}
			if got.Reasoning != info.Reasoning {
				t.Errorf("%s/%s: reverse lookup Reasoning = %v, want %v", provider, hclKey, got.Reasoning, info.Reasoning)
			}
		}
	}
}
