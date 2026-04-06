package aitools

import (
	"encoding/json"
	"strings"

	"squadron/llm"

	"github.com/zclconf/go-cty/cty"
)

// PropertyType represents a JSON Schema type
type PropertyType string

const (
	TypeString  PropertyType = "string"
	TypeNumber  PropertyType = "number"
	TypeInteger PropertyType = "integer"
	TypeBoolean PropertyType = "boolean"
	TypeArray   PropertyType = "array"
	TypeObject  PropertyType = "object"
)

// Property defines a single property in a JSON Schema
type Property struct {
	Type        PropertyType `json:"type"`
	Description string       `json:"description,omitempty"`
	Items       *Property    `json:"items,omitempty"`       // For array types
	Properties  PropertyMap  `json:"properties,omitempty"`  // For nested objects
	Required    []string     `json:"required,omitempty"`    // For nested objects
}

// PropertyMap is a map of property names to their definitions
type PropertyMap map[string]Property

// Schema represents a JSON Schema for tool parameters
type Schema struct {
	Type       PropertyType `json:"type"`
	Properties PropertyMap  `json:"properties"`
	Required   []string     `json:"required,omitempty"`
}

// String returns the JSON representation of the schema
func (s Schema) String() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ToJSONSchema returns the schema as a json.RawMessage suitable for ToolDefinition.InputSchema
func (s Schema) ToJSONSchema() json.RawMessage {
	b, _ := json.Marshal(s)
	return json.RawMessage(b)
}

// SanitizeToolName converts a tool map key (e.g. "plugins.shell.echo") into an
// API-safe name (e.g. "plugins_shell_echo"). Provider APIs restrict tool names to
// ^[a-zA-Z0-9_-]{1,64}$.
func SanitizeToolName(name string) string {
	return strings.ReplaceAll(name, ".", "_")
}

// ToolsToDefinitions converts a tool map to provider-agnostic ToolDefinition slice.
// Tool names are sanitized for API compatibility (dots replaced with underscores).
// Deduplicates by sanitized name so alias entries added by AddSanitizedAliases
// don't produce duplicate definitions.
func ToolsToDefinitions(tools map[string]Tool) []llm.ToolDefinition {
	seen := make(map[string]bool, len(tools))
	defs := make([]llm.ToolDefinition, 0, len(tools))
	for name, tool := range tools {
		sanitized := SanitizeToolName(name)
		if seen[sanitized] {
			continue
		}
		seen[sanitized] = true
		defs = append(defs, llm.ToolDefinition{
			Name:        sanitized,
			Description: tool.ToolDescription(),
			InputSchema: tool.ToolPayloadSchema().ToJSONSchema(),
		})
	}
	return defs
}

// AddSanitizedAliases adds sanitized name aliases to a tools map so that tools
// can be looked up by either their original key (e.g. "plugins.shell.echo") or
// their API-safe name (e.g. "plugins_shell_echo"). Entries where the sanitized
// name equals the original key are skipped.
func AddSanitizedAliases(tools map[string]Tool) {
	for name, tool := range tools {
		sanitized := SanitizeToolName(name)
		if sanitized != name {
			tools[sanitized] = tool
		}
	}
}

// ToCtyType converts the schema to a cty.Type for HCL evaluation
func (s Schema) ToCtyType() cty.Type {
	return propertyMapToCtyType(s.Properties)
}

// propertyMapToCtyType converts a PropertyMap to a cty object type
func propertyMapToCtyType(props PropertyMap) cty.Type {
	if len(props) == 0 {
		return cty.EmptyObject
	}

	attrTypes := make(map[string]cty.Type)
	for name, prop := range props {
		attrTypes[name] = propertyToCtyType(prop)
	}
	return cty.Object(attrTypes)
}

// propertyToCtyType converts a single Property to its cty.Type equivalent
func propertyToCtyType(p Property) cty.Type {
	switch p.Type {
	case TypeString:
		return cty.String
	case TypeNumber:
		return cty.Number
	case TypeInteger:
		return cty.Number
	case TypeBoolean:
		return cty.Bool
	case TypeArray:
		if p.Items != nil {
			return cty.List(propertyToCtyType(*p.Items))
		}
		return cty.List(cty.DynamicPseudoType)
	case TypeObject:
		if len(p.Properties) > 0 {
			return propertyMapToCtyType(p.Properties)
		}
		return cty.DynamicPseudoType
	default:
		return cty.DynamicPseudoType
	}
}
