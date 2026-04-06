package aitools

import (
	"encoding/json"

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

// ToolsToDefinitions converts a tool map to provider-agnostic ToolDefinition slice
func ToolsToDefinitions(tools map[string]Tool) []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		defs = append(defs, llm.ToolDefinition{
			Name:        tool.ToolName(),
			Description: tool.ToolDescription(),
			InputSchema: tool.ToolPayloadSchema().ToJSONSchema(),
		})
	}
	return defs
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
