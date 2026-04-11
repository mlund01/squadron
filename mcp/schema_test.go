package mcp

import (
	"testing"

	mcpproto "github.com/mark3labs/mcp-go/mcp"

	"squadron/aitools"
)

// TestConvertSchema_ObjectWithStringAndNumber verifies the happy path: an MCP
// tool input schema with a typed object containing primitive properties survives
// the round-trip into aitools.Schema with its type, properties, and required
// list intact.
func TestConvertSchema_ObjectWithStringAndNumber(t *testing.T) {
	in := mcpproto.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"path":    map[string]any{"type": "string"},
			"head":    map[string]any{"type": "number", "description": "first N lines"},
			"verbose": map[string]any{"type": "boolean"},
		},
		Required: []string{"path"},
	}

	out := convertSchema(in)

	if out.Type != aitools.TypeObject {
		t.Errorf("expected type=object, got %q", out.Type)
	}
	if len(out.Properties) != 3 {
		t.Errorf("expected 3 properties, got %d", len(out.Properties))
	}
	if p := out.Properties["path"]; p.Type != aitools.TypeString {
		t.Errorf("path.type = %q, want string", p.Type)
	}
	if p := out.Properties["head"]; p.Type != aitools.TypeNumber {
		t.Errorf("head.type = %q, want number", p.Type)
	}
	if p := out.Properties["head"]; p.Description != "first N lines" {
		t.Errorf("head.description = %q, want 'first N lines'", p.Description)
	}
	if p := out.Properties["verbose"]; p.Type != aitools.TypeBoolean {
		t.Errorf("verbose.type = %q, want boolean", p.Type)
	}
	if len(out.Required) != 1 || out.Required[0] != "path" {
		t.Errorf("required = %v, want [path]", out.Required)
	}
}

// TestConvertSchema_Empty covers the case an earlier bug exposed: the 2025.8.21
// version of @modelcontextprotocol/server-filesystem returned tools with an
// inputSchema that only set $schema and no properties. We want this to produce
// a valid empty aitools.Schema rather than a nil, and it must not panic.
func TestConvertSchema_Empty(t *testing.T) {
	in := mcpproto.ToolInputSchema{Type: ""}
	out := convertSchema(in)

	if out.Type != aitools.TypeObject {
		t.Errorf("empty schema should default to object type, got %q", out.Type)
	}
	if out.Properties == nil {
		t.Errorf("empty schema should have non-nil properties map")
	}
	if len(out.Properties) != 0 {
		t.Errorf("empty schema should have 0 properties, got %d", len(out.Properties))
	}
}

// TestConvertSchema_Array covers list-type fields, which are common in
// filesystem / search tools ("paths: string[]").
func TestConvertSchema_Array(t *testing.T) {
	in := mcpproto.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"paths": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		Required: []string{"paths"},
	}

	out := convertSchema(in)

	paths, ok := out.Properties["paths"]
	if !ok {
		t.Fatalf("paths property missing")
	}
	if paths.Type != aitools.TypeArray {
		t.Errorf("paths.type = %q, want array", paths.Type)
	}
	if paths.Items == nil {
		t.Fatalf("paths.items should not be nil")
	}
	if paths.Items.Type != aitools.TypeString {
		t.Errorf("paths.items.type = %q, want string", paths.Items.Type)
	}
}

// TestConvertSchema_NestedObject verifies object-in-object schemas come across
// with their nested properties intact.
func TestConvertSchema_NestedObject(t *testing.T) {
	in := mcpproto.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"options": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"recursive": map[string]any{"type": "boolean"},
					"depth":     map[string]any{"type": "integer"},
				},
				"required": []any{"recursive"},
			},
		},
	}

	out := convertSchema(in)

	opts, ok := out.Properties["options"]
	if !ok {
		t.Fatalf("options property missing")
	}
	if opts.Type != aitools.TypeObject {
		t.Errorf("options.type = %q, want object", opts.Type)
	}
	if len(opts.Properties) != 2 {
		t.Errorf("options should have 2 sub-properties, got %d", len(opts.Properties))
	}
	if opts.Properties["recursive"].Type != aitools.TypeBoolean {
		t.Errorf("options.recursive.type = %q, want boolean", opts.Properties["recursive"].Type)
	}
	if opts.Properties["depth"].Type != aitools.TypeInteger {
		t.Errorf("options.depth.type = %q, want integer", opts.Properties["depth"].Type)
	}
	if len(opts.Required) != 1 || opts.Required[0] != "recursive" {
		t.Errorf("options.required = %v, want [recursive]", opts.Required)
	}
}

// TestConvertSchema_NilProperties handles a server that returns a typed object
// with nil properties (seen in practice when a tool has no input parameters).
func TestConvertSchema_NilProperties(t *testing.T) {
	in := mcpproto.ToolInputSchema{Type: "object"}
	out := convertSchema(in)
	if out.Type != aitools.TypeObject {
		t.Errorf("type = %q, want object", out.Type)
	}
	if out.Properties == nil {
		t.Errorf("properties should be non-nil even when server omits them")
	}
}
