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

// convertSchema translates an MCP tool input schema into an aitools.Schema.
// Preserves the raw JSON Schema bytes via WithRawJSONSchema so the LLM sees
// the unabridged document — the typed projection is lossy (drops enum,
// oneOf, nullable type arrays, additionalProperties, $defs) and sending
// that to Anthropic's 2020-12 validator gets real tools rejected.
func convertSchema(in mcpproto.ToolInputSchema) aitools.Schema {
	raw, err := json.Marshal(in)
	if err != nil {
		return aitools.Schema{Type: aitools.TypeObject, Properties: aitools.PropertyMap{}}
	}

	// Best-effort projection for in-process readers (HCL cty conversion,
	// introspection). ToJSONSchema returns the raw bytes instead.
	bridge := map[string]any{
		"type":       in.Type,
		"properties": in.Properties,
	}
	if len(in.Required) > 0 {
		bridge["required"] = in.Required
	}
	bridgeBytes, err := json.Marshal(bridge)
	if err != nil {
		return aitools.Schema{Type: aitools.TypeObject, Properties: aitools.PropertyMap{}}.WithRawJSONSchema(raw)
	}
	var out aitools.Schema
	if err := json.Unmarshal(bridgeBytes, &out); err != nil {
		out = aitools.Schema{Type: aitools.TypeObject, Properties: aitools.PropertyMap{}}
	}
	if out.Type == "" {
		out.Type = aitools.TypeObject
	}
	if out.Properties == nil {
		out.Properties = aitools.PropertyMap{}
	}
	return out.WithRawJSONSchema(raw)
}
