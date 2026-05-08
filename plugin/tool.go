package plugin

import (
	"context"
	"encoding/json"

	"squadron/aitools"
)

type PluginTool struct {
	provider ToolProvider
	info     *ToolInfo
}

func NewPluginTool(provider ToolProvider, info *ToolInfo) *PluginTool {
	return &PluginTool{
		provider: provider,
		info:     info,
	}
}

func (t *PluginTool) ToolName() string {
	return t.info.Name
}

func (t *PluginTool) ToolDescription() string {
	return t.info.Description
}

func (t *PluginTool) ToolPayloadSchema() aitools.Schema {
	return t.info.Schema
}

func (t *PluginTool) ToolOutputSchema() json.RawMessage {
	return t.info.OutputSchema
}

func (t *PluginTool) Call(ctx context.Context, params string) string {
	result, err := t.provider.Call(ctx, t.info.Name, params)
	if err != nil {
		return "error: " + err.Error()
	}
	return result
}
