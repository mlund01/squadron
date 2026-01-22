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
var ReservedPluginNamespaces = []string{"bash", "http"}

// InternalPluginTools maps internal plugin namespaces to their tools
// These are accessed as plugins.bash.bash, plugins.http.get, etc.
var InternalPluginTools = map[string][]string{
	"bash": {"bash"},
	"http": {"get", "post", "put", "patch", "delete"},
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

// Agent represents an AI agent configuration
type Agent struct {
	Name        string    `hcl:"name,label"`
	Model       string    `hcl:"model"`
	Personality string    `hcl:"personality"`
	Role        string    `hcl:"role"`
	Tools       []string  `hcl:"tools,optional"`
	Mode        AgentMode `hcl:"mode,optional"`
}

// Validate checks that the agent configuration is valid
// Note: Toolbox tools are validated in Config.Validate() since we need access to custom tool definitions
func (a *Agent) Validate() error {
	// Default mode to chat if not specified
	if a.Mode == "" {
		a.Mode = ModeChat
	}

	// Validate mode
	if a.Mode != ModeChat && a.Mode != ModeWorkflow {
		return fmt.Errorf("invalid mode '%s': must be 'chat' or 'workflow'", a.Mode)
	}

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
