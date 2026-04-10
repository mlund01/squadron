package mcp

import (
	"encoding/json"

	mcpproto "github.com/mark3labs/mcp-go/mcp"

	"squadron/aitools"
)

// ToolInfo holds the translated metadata for one MCP tool, ready for
// consumption by the rest of squadron.
type ToolInfo struct {
	Name        string
	Description string
	Schema      aitools.Schema
}

// convertSchema best-effort converts an MCP tool input schema (raw JSON Schema)
// to aitools.Schema. MCP schemas are richer than aitools.Schema — fields like
// oneOf/allOf/enum/$ref lose information on the way through. We round-trip
// through JSON so anything that happens to line up (type, properties, required)
// comes across cleanly.
func convertSchema(in mcpproto.ToolInputSchema) aitools.Schema {
	bridge := map[string]any{
		"type":       in.Type,
		"properties": in.Properties,
	}
	if len(in.Required) > 0 {
		bridge["required"] = in.Required
	}
	raw, err := json.Marshal(bridge)
	if err != nil {
		return aitools.Schema{Type: aitools.TypeObject, Properties: aitools.PropertyMap{}}
	}
	var out aitools.Schema
	if err := json.Unmarshal(raw, &out); err != nil {
		return aitools.Schema{Type: aitools.TypeObject, Properties: aitools.PropertyMap{}}
	}
	if out.Type == "" {
		out.Type = aitools.TypeObject
	}
	if out.Properties == nil {
		out.Properties = aitools.PropertyMap{}
	}
	return out
}
