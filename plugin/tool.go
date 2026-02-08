package plugin

import "squadron/aitools"

// PluginTool wraps a plugin tool and implements the aitools.Tool interface
type PluginTool struct {
	provider ToolProvider
	info     *ToolInfo
}

// NewPluginTool creates a new PluginTool from a provider and tool info
func NewPluginTool(provider ToolProvider, info *ToolInfo) *PluginTool {
	return &PluginTool{
		provider: provider,
		info:     info,
	}
}

// ToolName returns the name of the tool
func (t *PluginTool) ToolName() string {
	return t.info.Name
}

// ToolDescription returns a description of what the tool does
func (t *PluginTool) ToolDescription() string {
	return t.info.Description
}

// ToolPayloadSchema returns the JSON schema for the tool's input parameters
func (t *PluginTool) ToolPayloadSchema() aitools.Schema {
	return t.info.Schema
}

// Call executes the tool with the given parameters and returns a stringified response
func (t *PluginTool) Call(params string) string {
	result, err := t.provider.Call(t.info.Name, params)
	if err != nil {
		return "error: " + err.Error()
	}
	return result
}
