package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaseHCL_ContainsExpectedBlocks(t *testing.T) {
	p := providers[0] // anthropic
	hcl := baseHCL(p, true)

	requiredFragments := []string{
		`variable "anthropic_api_key"`,
		`secret = true`,
		`model "anthropic"`,
		`provider = "anthropic"`,
		`api_key  = vars.anthropic_api_key`,
		`agent "researcher"`,
		`models.anthropic.claude_haiku_4_5`,
		`tools       = [builtins.http.get]`,
	}
	for _, fragment := range requiredFragments {
		if !strings.Contains(hcl, fragment) {
			t.Errorf("baseHCL missing fragment %q", fragment)
		}
	}
}

func TestBaseHCL_OmitsAgentWithoutStarter(t *testing.T) {
	hcl := baseHCL(providers[0], false)
	if strings.Contains(hcl, `agent "researcher"`) {
		t.Error("baseHCL should not include researcher agent when starter mission is not requested")
	}
	if strings.Contains(hcl, `builtins.http.get`) {
		t.Error("baseHCL should not reference starter-only tools when starter mission is not requested")
	}
}

func TestBaseHCL_AllProviders(t *testing.T) {
	for _, p := range providers {
		t.Run(p.Provider, func(t *testing.T) {
			hcl := baseHCL(p, true)
			if !strings.Contains(hcl, `provider = "`+p.Provider+`"`) {
				t.Errorf("missing provider %q", p.Provider)
			}
			if !strings.Contains(hcl, `vars.`+p.VarName) {
				t.Errorf("missing var ref %q", p.VarName)
			}
			if !strings.Contains(hcl, `models.`+p.Provider+`.`+p.ModelKey) {
				t.Errorf("missing model %q.%q", p.Provider, p.ModelKey)
			}
		})
	}
}

func TestStarterMissionHCL_ContainsTasks(t *testing.T) {
	hcl := starterMissionHCL(providers[0])

	requiredFragments := []string{
		`mission "hn_research"`,
		`task "discover"`,
		`task "research"`,
		`task "summarize"`,
		`depends_on = [tasks.discover]`,
		`depends_on = [tasks.research]`,
		`parallel = true`,
		`news.ycombinator.com`,
	}
	for _, fragment := range requiredFragments {
		if !strings.Contains(hcl, fragment) {
			t.Errorf("starterMissionHCL missing fragment %q", fragment)
		}
	}
}

func TestGenerateStarterConfig_WithoutStarter(t *testing.T) {
	dir := t.TempDir()
	if err := generateStarterConfig(dir, providers[0], false); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "squadron.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, `model "anthropic"`) {
		t.Error("expected model block")
	}
	if strings.Contains(content, `mission "hn_research"`) {
		t.Error("should not contain starter mission")
	}
	if strings.Contains(content, `agent "researcher"`) {
		t.Error("should not contain researcher agent without starter mission")
	}
}

func TestGenerateStarterConfig_WithStarter(t *testing.T) {
	dir := t.TempDir()
	if err := generateStarterConfig(dir, providers[0], true); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "squadron.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, `model "anthropic"`) {
		t.Error("expected model block")
	}
	if !strings.Contains(content, `mission "hn_research"`) {
		t.Error("expected starter mission")
	}
}

func TestGenerateStarterConfig_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "squadron.hcl")
	if err := os.WriteFile(existing, []byte("# keep me"), 0644); err != nil {
		t.Fatal(err)
	}

	err := generateStarterConfig(dir, providers[0], true)
	if err == nil {
		t.Fatal("expected error when file already exists")
	}

	// Verify the existing file was not overwritten.
	data, _ := os.ReadFile(existing)
	if string(data) != "# keep me" {
		t.Error("existing file was overwritten")
	}
}
