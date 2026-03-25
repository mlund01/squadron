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

// ReservedBuiltinNamespaces are names reserved for built-in tools (cannot be used as plugin names).
var ReservedBuiltinNamespaces = []string{"bash", "http", "dataset", "utils"}

// BuiltinTools maps built-in namespaces to their tools.
// These are accessed as builtins.bash.bash, builtins.http.get, etc.
var BuiltinTools = map[string][]string{
	"bash":    {"bash"},
	"http":    {"get", "post", "put", "patch", "delete"},
	"dataset": {"set", "sample", "count"},
	"utils":   {"sleep"},
}

// InternalTools is the list of available internal tools (legacy format for backwards compatibility)
// Deprecated: Use BuiltinTools instead
var InternalTools = []string{
	"bash",
	"http_get",
	"http_post",
	"http_put",
	"http_patch",
	"http_delete",
}

// IsReservedBuiltinNamespace checks if a name is reserved for built-in tools (cannot be used as plugin name)
func IsReservedBuiltinNamespace(name string) bool {
	for _, n := range ReservedBuiltinNamespaces {
		if n == name {
			return true
		}
	}
	return false
}

// IsBuiltinTool checks if a tool reference (e.g., "builtins.http.get") is a built-in tool
func IsBuiltinTool(ref string) bool {
	parts := strings.Split(ref, ".")
	if len(parts) != 3 || parts[0] != "builtins" {
		return false
	}
	pluginName := parts[1]
	toolName := parts[2]

	tools, ok := BuiltinTools[pluginName]
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
// Deprecated: Use IsBuiltinTool instead
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
	// PruneOn: trigger pruning when conversation reaches this many turns (0 = disabled)
	PruneOn int `hcl:"prune_on,optional"`
	// PruneTo: when pruning triggers, reduce conversation to this many turns
	PruneTo int `hcl:"prune_to,optional"`
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

// GetPruneOn returns the prune_on threshold (0 = disabled)
func (a *Agent) GetPruneOn() int {
	if a.Pruning == nil {
		return 0
	}
	return a.Pruning.PruneOn
}

// GetPruneTo returns the prune_to target (0 = disabled)
func (a *Agent) GetPruneTo() int {
	if a.Pruning == nil {
		return 0
	}
	return a.Pruning.PruneTo
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
