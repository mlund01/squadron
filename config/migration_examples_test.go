package config_test

import (
	"path/filepath"
	"testing"

	"squadron/config"
	"squadron/config/runtimemodels"
)

func TestMissionMigrationExamplesValidate(t *testing.T) {
	runtimemodels.Replace(map[string]runtimemodels.Connection{
		"anthropic": {Provider: "anthropic", APIKey: "test-key", PromptCaching: true},
	})
	t.Cleanup(func() { runtimemodels.Replace(nil) })
	path := filepath.Join("..", "docs", "examples", "mission-migration.hcl")
	cfg, err := config.LoadAndValidate(path)
	if err != nil {
		t.Fatalf("migration examples must validate: %v", err)
	}
	want := map[string]bool{
		"linear_example": true, "parallel_example": true, "routed_example": true,
		"iterative_example": true, "retry_example": true, "human_example": true,
	}
	for _, mission := range cfg.Missions {
		delete(want, mission.Name)
	}
	if len(want) != 0 {
		t.Fatalf("migration examples missing missions: %v", want)
	}
}
