package config

import (
	"strings"
	"testing"
)

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

func TestBuiltinReasoningSupport(t *testing.T) {
	cases := []struct {
		provider Provider
		model    string
		want     bool
	}{
		// Anthropic — Claude 4+ supports extended thinking, 3.x does not.
		{ProviderAnthropic, "claude-opus-4-7", true},
		{ProviderAnthropic, "claude-sonnet-4-6", true},
		{ProviderAnthropic, "claude-haiku-4-5-20251001", true},
		{ProviderAnthropic, "claude-3-5-sonnet-20241022", false},
		{ProviderAnthropic, "claude-3-5-haiku-20241022", false},
		// OpenAI — o3/o4/gpt-5 family. o1 excluded (rejects param).
		{ProviderOpenAI, "o3", true},
		{ProviderOpenAI, "o3-mini", true},
		{ProviderOpenAI, "o4-mini", true},
		{ProviderOpenAI, "gpt-5", true},
		{ProviderOpenAI, "gpt-5.5-pro", true},
		{ProviderOpenAI, "o1", false},
		{ProviderOpenAI, "o1-mini", false},
		{ProviderOpenAI, "gpt-4o", false},
		{ProviderOpenAI, "gpt-4.1", false},
		// Gemini — 2.5+ and 3.x.
		{ProviderGemini, "gemini-2.5-pro", true},
		{ProviderGemini, "gemini-2.5-flash", true},
		{ProviderGemini, "gemini-3.1-pro-preview", true},
		{ProviderGemini, "gemini-2.0-flash", false},
		{ProviderGemini, "gemini-1.5-pro", false},
		// Ollama — never via prefix; user must declare.
		{ProviderOllama, "deepseek-r1", false},
		{ProviderOllama, "qwen3", false},
		{ProviderOllama, "llama3", false},
	}
	for _, tc := range cases {
		got := builtinReasoningSupport(tc.provider, tc.model)
		if got != tc.want {
			t.Errorf("builtinReasoningSupport(%v, %q) = %v, want %v", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestModelSupportsReasoning_OverrideForcesEnabled(t *testing.T) {
	// Ollama model with no built-in support — user declares deepseek_r1
	// as reasoning-capable; llama3 is not declared.
	m := &Model{
		Provider: ProviderOllama,
		BaseURL:  "http://localhost:11434/v1",
		Aliases: map[string]string{
			"deepseek_r1": "deepseek-r1:7b",
			"llama3":      "llama3:8b",
		},
		ReasoningModels: []string{"deepseek_r1"},
	}

	if !ModelSupportsReasoning(m, "deepseek-r1:7b") {
		t.Error("expected deepseek-r1:7b to be supported via reasoning_models override")
	}
	if ModelSupportsReasoning(m, "llama3:8b") {
		t.Error("llama3:8b is not declared and Ollama has no built-in support — should be false")
	}
}

func TestModelSupportsReasoning_OverrideAcceptsApiNames(t *testing.T) {
	// Override entries can be either alias keys or raw API names.
	m := &Model{
		Provider: ProviderOllama,
		BaseURL:  "http://localhost:11434/v1",
		Aliases: map[string]string{
			"qwen3": "qwen3:8b",
		},
		ReasoningModels: []string{"qwen3:8b"},
	}
	if !ModelSupportsReasoning(m, "qwen3:8b") {
		t.Error("expected qwen3:8b to be supported via API-name override")
	}
}

func TestModelSupportsReasoning_BuiltinFallback(t *testing.T) {
	// Anthropic with no override — falls back to built-in detector.
	m := &Model{
		Provider: ProviderAnthropic,
		APIKey:   "test",
	}
	if !ModelSupportsReasoning(m, "claude-opus-4-7") {
		t.Error("Claude 4 should be supported via built-in detector")
	}
	if ModelSupportsReasoning(m, "claude-3-5-sonnet-20241022") {
		t.Error("Claude 3.5 should NOT be supported")
	}
}

func TestModel_Validate_RejectsUnknownReasoningModel(t *testing.T) {
	m := &Model{
		Provider: ProviderOllama,
		BaseURL:  "http://localhost:11434/v1",
		Aliases: map[string]string{
			"qwen3": "qwen3:8b",
		},
		ReasoningModels: []string{"missing_alias"},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "missing_alias") {
		t.Errorf("expected validation error mentioning missing_alias, got %v", err)
	}
}

