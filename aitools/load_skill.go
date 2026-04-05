package aitools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// SkillDefinition holds a resolved skill available to an agent at runtime.
type SkillDefinition struct {
	Name         string
	Description  string
	Instructions string
	ToolRefs     []string
}

// SkillSessionManager is the interface for injecting system prompts at runtime.
type SkillSessionManager interface {
	AddSystemPrompt(prompt string)
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

	// Inject skill instructions + tool definitions as a system prompt
	if t.mgr.Session != nil {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# Skill: %s\n\n", skill.Name))
		if skill.Instructions != "" {
			sb.WriteString(skill.Instructions)
			sb.WriteString("\n\n")
		}
		if len(state.toolNames) > 0 {
			sb.WriteString("## Tools Added by This Skill\n\n")
			for _, name := range state.toolNames {
				if tool, ok := t.mgr.AgentTools[name]; ok {
					sb.WriteString(fmt.Sprintf("### %s\n\n", name))
					sb.WriteString(fmt.Sprintf("%s\n\n", tool.ToolDescription()))
					sb.WriteString(fmt.Sprintf("**Input Schema:**\n```json\n%s\n```\n\n", tool.ToolPayloadSchema().String()))
				}
			}
		}
		state.systemPrompt = sb.String()
		t.mgr.Session.AddSystemPrompt(state.systemPrompt)
	}

	t.mgr.LoadedSkills[input.Name] = state

	return fmt.Sprintf("Skill '%s' loaded. Instructions and tools are now available.", input.Name)
}

