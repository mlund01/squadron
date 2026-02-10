package config

import (
	"fmt"
	"strings"
)

// AgentMode defines the operational mode of the agent
type AgentMode string

const (
	// ModeChat is for interactive user chat sessions where reasoning is optional
	ModeChat AgentMode = "chat"
	// ModeMission is for automated missions where the agent continuously reasons
	// and acts until the task is complete
	ModeMission AgentMode = "mission"
)

// ReservedPluginNamespaces are plugin names reserved for internal tools
var ReservedPluginNamespaces = []string{"bash", "http", "dataset"}

// InternalPluginTools maps internal plugin namespaces to their tools
// These are accessed as plugins.bash.bash, plugins.http.get, etc.
var InternalPluginTools = map[string][]string{
	"bash":    {"bash"},
	"http":    {"get", "post", "put", "patch", "delete"},
	"dataset": {"set", "sample", "count"},
}

// InternalTools is the list of available internal tools (legacy format for backwards compatibility)
// Deprecated: Use InternalPluginTools instead
var InternalTools = []string{
	"bash",
	"http_get",
	"http_post",
	"http_put",
	"http_patch",
	"http_delete",
}

// IsReservedPluginNamespace checks if a plugin name is reserved for internal tools
func IsReservedPluginNamespace(name string) bool {
	for _, n := range ReservedPluginNamespaces {
		if n == name {
			return true
		}
	}
	return false
}

// IsInternalPluginTool checks if a tool reference (e.g., "plugins.http.get") is an internal tool
func IsInternalPluginTool(ref string) bool {
	parts := strings.Split(ref, ".")
	if len(parts) != 3 || parts[0] != "plugins" {
		return false
	}
	pluginName := parts[1]
	toolName := parts[2]

	tools, ok := InternalPluginTools[pluginName]
	if !ok {
		return false
	}
	for _, t := range tools {
		if t == toolName {
			return true
		}
	}
	return false
}

// IsInternalTool checks if a tool name is a reserved internal tool (legacy format)
// Deprecated: Use IsInternalPluginTool instead
func IsInternalTool(name string) bool {
	for _, t := range InternalTools {
		if t == name {
			return true
		}
	}
	return false
}

// Compaction configures context compaction for an agent
type Compaction struct {
	TokenLimit    int `hcl:"token_limit"`    // Trigger compaction when input tokens exceed this
	TurnRetention int `hcl:"turn_retention"` // Keep this many recent turns uncompacted
}

// Pruning configures context pruning for an agent
type Pruning struct {
	// SingleToolLimit: keep only the last N results from each tool (0 = disabled)
	SingleToolLimit int `hcl:"single_tool_limit,optional"`
	// AllToolLimit: prune tool results older than N messages ago (0 = disabled)
	AllToolLimit int `hcl:"all_tool_limit,optional"`
	// TurnLimit: rolling window - drop messages older than N turns (0 = disabled)
	// Unlike other pruning, this removes messages entirely (no replacement text)
	TurnLimit int `hcl:"turn_limit,optional"`
}

// Agent represents an AI agent configuration
type Agent struct {
	Name        string   `hcl:"name,label"`
	Model       string   `hcl:"model"`
	Personality string   `hcl:"personality"`
	Role        string   `hcl:"role"`
	Tools       []string `hcl:"tools,optional"`

	// Pruning settings (optional block)
	Pruning *Pruning `hcl:"pruning,block"`

	// Compaction settings (optional block)
	Compaction *Compaction `hcl:"compaction,block"`
}

// GetSingleToolLimit returns the single tool limit (0 = disabled)
func (a *Agent) GetSingleToolLimit() int {
	if a.Pruning == nil {
		return 0
	}
	return a.Pruning.SingleToolLimit
}

// GetAllToolLimit returns the all tool limit (0 = disabled)
func (a *Agent) GetAllToolLimit() int {
	if a.Pruning == nil {
		return 0
	}
	return a.Pruning.AllToolLimit
}

// GetTurnLimit returns the turn limit for rolling window pruning (0 = disabled)
func (a *Agent) GetTurnLimit() int {
	if a.Pruning == nil {
		return 0
	}
	return a.Pruning.TurnLimit
}

// Validate checks that the agent configuration is valid
// Note: Toolbox tools are validated in Config.Validate() since we need access to custom tool definitions
func (a *Agent) Validate() error {
	return nil
}

// ResolveModel finds the Model config that matches this agent's model key
func (a *Agent) ResolveModel(models []Model) (*Model, string, error) {
	// a.Model is the model key (e.g., "claude_sonnet_4")
	// Find which provider supports this model and get the actual model name
	for i := range models {
		m := &models[i]
		supportedModels, ok := SupportedModels[m.Provider]
		if !ok {
			continue
		}

		// Check if this model key is in the provider's allowed models
		for _, allowedKey := range m.AllowedModels {
			if allowedKey == a.Model {
				actualModel, ok := supportedModels[a.Model]
				if !ok {
					return nil, "", fmt.Errorf("model key '%s' not found in supported models for provider '%s'", a.Model, m.Provider)
				}
				return m, actualModel, nil
			}
		}
	}

	return nil, "", fmt.Errorf("no model config found for model '%s'", a.Model)
}
