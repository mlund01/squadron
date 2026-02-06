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
	// ModeWorkflow is for automated workflows where the agent continuously reasons
	// and acts until the task is complete
	ModeWorkflow AgentMode = "workflow"
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

// Default pruning limits
const (
	DefaultToolRecencyLimit    = 3
	DefaultMessageRecencyLimit = 20
)

// Agent represents an AI agent configuration
type Agent struct {
	Name        string   `hcl:"name,label"`
	Model       string   `hcl:"model"`
	Personality string   `hcl:"personality"`
	Role        string   `hcl:"role"`
	Tools       []string `hcl:"tools,optional"`

	// Pruning defaults: how many recent results to keep per tool, and how
	// many messages back to keep tool results. LLM can override per-call.
	// -1 disables that pruning dimension. 0 means "use default".
	ToolRecencyLimit    int `hcl:"tool_recency_limit,optional"`
	MessageRecencyLimit int `hcl:"message_recency_limit,optional"`
}

// GetToolRecencyLimit returns the effective tool recency limit (applying defaults)
func (a *Agent) GetToolRecencyLimit() int {
	if a.ToolRecencyLimit < 0 {
		return 0 // disabled
	}
	if a.ToolRecencyLimit == 0 {
		return DefaultToolRecencyLimit
	}
	return a.ToolRecencyLimit
}

// GetMessageRecencyLimit returns the effective message recency limit (applying defaults)
func (a *Agent) GetMessageRecencyLimit() int {
	if a.MessageRecencyLimit < 0 {
		return 0 // disabled
	}
	if a.MessageRecencyLimit == 0 {
		return DefaultMessageRecencyLimit
	}
	return a.MessageRecencyLimit
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
