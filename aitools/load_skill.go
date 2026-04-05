package aitools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// SkillDefinition holds a resolved skill available to an agent at runtime.
type SkillDefinition struct {
	Name         string
	Description  string
	Instructions string
	ToolRefs     []string
}

// SkillSessionManager is the interface for injecting/removing system prompts at runtime.
type SkillSessionManager interface {
	AddSystemPrompt(prompt string)
	RemoveSystemPrompt(prompt string)
}

// SkillState tracks what was added when a skill was loaded.
type SkillState struct {
	toolNames    []string
	systemPrompt string
}

// SkillManager is shared between LoadSkillTool and UnloadSkillTool.
type SkillManager struct {
	mu              sync.Mutex
	AvailableSkills map[string]*SkillDefinition
	AgentTools      map[string]Tool
	ToolBuilder     func(toolRefs []string) map[string]Tool
	LoadedSkills    map[string]*SkillState
	Session         SkillSessionManager
}

// LoadSkillTool allows agents to load skills on-demand.
type LoadSkillTool struct {
	mgr *SkillManager
}

func NewLoadSkillTool(mgr *SkillManager) *LoadSkillTool {
	return &LoadSkillTool{mgr: mgr}
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

	t.mgr.mu.Lock()
	defer t.mgr.mu.Unlock()

	if _, loaded := t.mgr.LoadedSkills[input.Name]; loaded {
		return fmt.Sprintf("Skill '%s' is already loaded.", input.Name)
	}

	skill, ok := t.mgr.AvailableSkills[input.Name]
	if !ok {
		var available []string
		for name := range t.mgr.AvailableSkills {
			available = append(available, name)
		}
		return fmt.Sprintf("Error: skill '%s' not found. Available skills: %v", input.Name, available)
	}

	state := &SkillState{}

	// Build and inject the skill's tools
	if len(skill.ToolRefs) > 0 {
		newTools := t.mgr.ToolBuilder(skill.ToolRefs)
		for name, tool := range newTools {
			t.mgr.AgentTools[name] = tool
			state.toolNames = append(state.toolNames, name)
		}
	}

	// Inject skill instructions as a system prompt
	if t.mgr.Session != nil && skill.Instructions != "" {
		state.systemPrompt = fmt.Sprintf("# Skill: %s\n\n%s", skill.Name, skill.Instructions)
		t.mgr.Session.AddSystemPrompt(state.systemPrompt)
	}

	t.mgr.LoadedSkills[input.Name] = state

	return fmt.Sprintf("Skill '%s' loaded. Instructions and tools are now available.", input.Name)
}

// UnloadSkillTool allows agents to unload a previously loaded skill.
type UnloadSkillTool struct {
	mgr *SkillManager
}

func NewUnloadSkillTool(mgr *SkillManager) *UnloadSkillTool {
	return &UnloadSkillTool{mgr: mgr}
}

func (t *UnloadSkillTool) ToolName() string { return "unload_skill" }

func (t *UnloadSkillTool) ToolDescription() string {
	return "Unload a previously loaded skill. Removes the skill's tools and instructions from your context."
}

func (t *UnloadSkillTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"name": {
				Type:        TypeString,
				Description: "Name of the skill to unload",
			},
		},
		Required: []string{"name"},
	}
}

func (t *UnloadSkillTool) Call(ctx context.Context, params string) string {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(params), &input); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	t.mgr.mu.Lock()
	defer t.mgr.mu.Unlock()

	state, loaded := t.mgr.LoadedSkills[input.Name]
	if !loaded {
		return fmt.Sprintf("Error: skill '%s' is not loaded.", input.Name)
	}

	// Remove the skill's tools
	for _, name := range state.toolNames {
		delete(t.mgr.AgentTools, name)
	}

	// Remove the skill's system prompt
	if t.mgr.Session != nil && state.systemPrompt != "" {
		t.mgr.Session.RemoveSystemPrompt(state.systemPrompt)
	}

	delete(t.mgr.LoadedSkills, input.Name)

	return fmt.Sprintf("Skill '%s' unloaded. Its tools and instructions have been removed.", input.Name)
}
