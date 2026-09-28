package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"squadron/config"
	"squadron/config/runtimemodels"
)

func TestBuildMissionGuideExampleValidates(t *testing.T) {
	path := filepath.Join("..", "docs", "content", "guides", "build-a-mission.mdx")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(content), "```hcl\n")
	if len(parts) != 3 {
		t.Fatalf("expected two HCL blocks in guide, got %d", len(parts)-1)
	}
	var blocks []string
	for _, part := range parts[1:] {
		block, _, found := strings.Cut(part, "\n```")
		if !found {
			t.Fatal("unterminated HCL block in guide")
		}
		blocks = append(blocks, block)
	}
	configPath := filepath.Join(t.TempDir(), "guide.hcl")
	if err := os.WriteFile(configPath, []byte(strings.Join(blocks, "\n\n")), 0600); err != nil {
		t.Fatal(err)
	}
	runtimemodels.Replace(map[string]runtimemodels.Connection{
		"anthropic": {Provider: "anthropic", APIKey: "test-key", PromptCaching: true},
	})
	t.Cleanup(func() { runtimemodels.Replace(nil) })
	cfg, err := config.LoadAndValidate(configPath)
	if err != nil {
		t.Fatalf("guide example must validate: %v", err)
	}
	if len(cfg.Missions) != 1 || cfg.Missions[0].Name != "triage" {
		t.Fatalf("unexpected guide missions: %+v", cfg.Missions)
	}
}
