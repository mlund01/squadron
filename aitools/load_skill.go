package aitools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// SkillDefinition holds a resolved skill available to an agent at runtime.
type SkillDefinition struct {
	Name        string
	Description string
	Instructions     string
	ToolRefs    []string
}

// SystemPromptAdder is the interface for injecting system prompts at runtime.
type SystemPromptAdder interface {
	AddSystemPrompt(prompt string)
}

// LoadSkillTool allows agents to load skills on-demand.
type LoadSkillTool struct {
	mu              sync.Mutex
	AvailableSkills map[string]*SkillDefinition
	AgentTools      map[string]Tool
	ToolBuilder     func(toolRefs []string) map[string]Tool
	LoadedSkills    map[string]bool
	Session         SystemPromptAdder
}

func (t *LoadSkillTool) ToolName() string { return "load_skill" }

func (t *LoadSkillTool) ToolDescription() string {
	return "Load a skill to gain new capabilities and detailed instructions. Once loaded, the skill's tools become available and its instructions are injected as context."
}

func (t *LoadSkillTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"name": {
				Type:        TypeString,
				Description: "Name of the skill to load",
			},
		},
		Required: []string{"name"},
	}
}

func (t *LoadSkillTool) Call(ctx context.Context, params string) string {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(params), &input); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.LoadedSkills[input.Name] {
		return fmt.Sprintf("Skill '%s' is already loaded.", input.Name)
	}

	skill, ok := t.AvailableSkills[input.Name]
	if !ok {
		var available []string
		for name := range t.AvailableSkills {
			available = append(available, name)
		}
		return fmt.Sprintf("Error: skill '%s' not found. Available skills: %v", input.Name, available)
	}

	// Build and inject the skill's tools
	if len(skill.ToolRefs) > 0 {
		newTools := t.ToolBuilder(skill.ToolRefs)
		for name, tool := range newTools {
			t.AgentTools[name] = tool
		}
	}

	// Inject skill details as a system prompt
	if t.Session != nil && skill.Instructions != "" {
		t.Session.AddSystemPrompt(fmt.Sprintf("# Skill: %s\n\n%s", skill.Name, skill.Instructions))
	}

	t.LoadedSkills[input.Name] = true

	return fmt.Sprintf("Skill '%s' loaded. Instructions and tools are now available.", input.Name)
}
