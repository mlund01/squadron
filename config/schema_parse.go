package config

import (
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"
)

// parseSchemaObject parses a cty.ObjectVal produced by an HCL object expression of the form:
//
//	inputs = {
//	  city  = string("Target city", true)
//	  count = number("How many results")
//	  tags  = list(string, "Tags to apply")
//	}
//
// Returns []InputField sorted by name for deterministic ordering.
// Used for tool inputs and dataset schema shorthand.
func parseSchemaObject(val cty.Value) ([]InputField, error) {
	if !val.Type().IsObjectType() {
		return nil, fmt.Errorf("expected an object expression { key = type(...) }, got %s", val.Type().FriendlyName())
	}

	names := sortedAttrNames(val)
	var fields []InputField
	for _, name := range names {
		field, err := parseSchemaNode(name, val.GetAttr(name))
		if err != nil {
			return nil, err
		}
		fields = append(fields, *field)
	}
	return fields, nil
}

// parseOutputSchemaObject parses a cty.ObjectVal for task output schema shorthand:
//
//	output = {
//	  summary    = string("Research summary", true)
//	  count      = number("Result count", true)
//	  categories = list(string, "Category names")
//	}
//
// Returns []OutputField sorted by name for deterministic ordering.
func parseOutputSchemaObject(val cty.Value) ([]OutputField, error) {
	if !val.Type().IsObjectType() {
		return nil, fmt.Errorf("expected an object expression { key = type(...) }, got %s", val.Type().FriendlyName())
	}

	names := sortedAttrNames(val)
	var fields []OutputField
	for _, name := range names {
		field, err := parseOutputSchemaNode(name, val.GetAttr(name))
		if err != nil {
			return nil, err
		}
		fields = append(fields, *field)
	}
	return fields, nil
}

// parseSchemaNode recursively parses a single schema node cty.Value into an InputField.
// A schema node is a cty.ObjectVal produced by one of the schema helper functions
// (string, number, integer, bool, list, map, object) with a "kind" attribute.
func parseSchemaNode(name string, val cty.Value) (*InputField, error) {
	if !val.Type().IsObjectType() || !val.Type().HasAttribute("kind") {
		return nil, fmt.Errorf(
			"field %q: expected schema helper function call (e.g. string(\"desc\"), list(string, \"desc\")), got %s",
			name, val.Type().FriendlyName(),
		)
	}

	kind := val.GetAttr("kind").AsString()
	desc := schemaNodeString(val, "description")
	required := schemaNodeBool(val, "required")

	field := &InputField{
		Name:        name,
		Type:        schemaKindToInputType(kind),
		Description: desc,
		Required:    required,
	}

	switch kind {
	case "list", "map":
		if val.Type().HasAttribute("items") {
			items, err := parseSchemaNode("", val.GetAttr("items"))
			if err != nil {
				return nil, fmt.Errorf("field %q items: %w", name, err)
			}
			field.Items = items
		}
	case "object":
		if val.Type().HasAttribute("properties") {
			props, err := parseSchemaObject(val.GetAttr("properties"))
			if err != nil {
				return nil, fmt.Errorf("field %q properties: %w", name, err)
			}
			field.Properties = props
		}
	}

	return field, nil
}

// parseOutputSchemaNode recursively parses a single schema node cty.Value into an OutputField.
func parseOutputSchemaNode(name string, val cty.Value) (*OutputField, error) {
	if !val.Type().IsObjectType() || !val.Type().HasAttribute("kind") {
		return nil, fmt.Errorf(
			"field %q: expected schema helper function call (e.g. string(\"desc\"), list(string, \"desc\")), got %s",
			name, val.Type().FriendlyName(),
		)
	}

	kind := val.GetAttr("kind").AsString()
	desc := schemaNodeString(val, "description")
	required := schemaNodeBool(val, "required")

	field := &OutputField{
		Name:        name,
		Type:        schemaKindToInputType(kind),
		Description: desc,
		Required:    required,
	}

	switch kind {
	case "list", "map":
		if val.Type().HasAttribute("items") {
			items, err := parseOutputSchemaNode("", val.GetAttr("items"))
			if err != nil {
				return nil, fmt.Errorf("field %q items: %w", name, err)
			}
			field.Items = items
		}
	case "object":
		if val.Type().HasAttribute("properties") {
			props, err := parseOutputSchemaObject(val.GetAttr("properties"))
			if err != nil {
				return nil, fmt.Errorf("field %q properties: %w", name, err)
			}
			field.Properties = props
		}
	}

	return field, nil
}

// parseSchemaObjectAsMissionInputs parses a cty.ObjectVal for mission inputs shorthand:
//
//	inputs = {
//	  complaint = string("The original complaint", true)
//	  severity  = string("Severity level", { default = "high" })
//	  api_key   = string("OpenAI API key", { secret = true })
//	  limit     = number("Max results", { default = 10 })
//	}
//
// Returns []MissionInput sorted by name for deterministic ordering.
// The options object may carry "default" (value) and "secret" (bool) extra attributes.
func parseSchemaObjectAsMissionInputs(val cty.Value) ([]MissionInput, error) {
	if !val.Type().IsObjectType() {
		return nil, fmt.Errorf("expected an object expression { key = type(...) }, got %s", val.Type().FriendlyName())
	}

	names := sortedAttrNames(val)
	var inputs []MissionInput
	for _, name := range names {
		input, err := parseSchemaNodeAsMissionInput(name, val.GetAttr(name))
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, *input)
	}
	return inputs, nil
}

// parseSchemaNodeAsMissionInput parses a single schema node into a MissionInput.
// Handles the "default" and "secret" extra attributes set by the options object.
func parseSchemaNodeAsMissionInput(name string, val cty.Value) (*MissionInput, error) {
	if !val.Type().IsObjectType() || !val.Type().HasAttribute("kind") {
		return nil, fmt.Errorf(
			"input %q: expected schema helper function call (e.g. string(\"desc\"), string(\"desc\", { default = \"val\" })), got %s",
			name, val.Type().FriendlyName(),
		)
	}

	kind := val.GetAttr("kind").AsString()
	desc := schemaNodeString(val, "description")

	input := &MissionInput{
		Name:        name,
		Type:        kind,
		Description: desc,
	}

	// "default" extra attribute from options object: { default = "val" }
	if val.Type().HasAttribute("default") {
		dv := val.GetAttr("default")
		if !dv.IsNull() {
			input.Default = &dv
		}
	}

	// "secret" boolean flag from options object: { secret = true }
	if val.Type().HasAttribute("secret") {
		sv := val.GetAttr("secret")
		if sv == cty.True {
			input.Secret = true
		}
	}

	return input, nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

// schemaKindToInputType maps a schema "kind" string to the InputField/OutputField type string.
// The type strings match what aitools.PropertyType and stringToPropertyType() expect.
func schemaKindToInputType(kind string) string {
	switch kind {
	case "string":
		return "string"
	case "number":
		return "number"
	case "integer":
		return "integer"
	case "bool":
		return "boolean"
	case "list":
		return "array"
	case "object", "map":
		// JSON Schema uses "object" for both structured objects and free-form maps
		return "object"
	case "any", "any_primitive":
		// Used as inner type refs for list/map — no JSON Schema type constraint
		return kind
	default:
		return kind
	}
}

// schemaNodeString safely reads a string attribute from a schema node cty.Value.
func schemaNodeString(val cty.Value, attr string) string {
	if !val.Type().HasAttribute(attr) {
		return ""
	}
	v := val.GetAttr(attr)
	if v.IsNull() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}

// schemaNodeBool safely reads a bool attribute from a schema node cty.Value.
func schemaNodeBool(val cty.Value, attr string) bool {
	if !val.Type().HasAttribute(attr) {
		return false
	}
	v := val.GetAttr(attr)
	if v.IsNull() || v.Type() != cty.Bool {
		return false
	}
	return v.True()
}

// sortedAttrNames returns the attribute names of a cty.Object value sorted alphabetically.
// This ensures deterministic field ordering when iterating over the schema object.
func sortedAttrNames(val cty.Value) []string {
	attrTypes := val.Type().AttributeTypes()
	names := make([]string, 0, len(attrTypes))
	for name := range attrTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
