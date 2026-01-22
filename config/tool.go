package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"squad/aitools"
	"squad/plugin"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// CustomTool represents a user-defined tool that wraps an internal tool
type CustomTool struct {
	Name        string                  `hcl:"name,label"`
	Implements  string                  `hcl:"implements"`
	Description string                  `hcl:"description,optional"`
	Inputs      *InputsSchema           `hcl:"inputs,block"`
	FieldExprs  map[string]hcl.Expression // Dynamic field expressions from the implemented tool's schema
}

// InputsSchema defines the custom inputs for the tool using attribute-based syntax
type InputsSchema struct {
	Fields []InputField
}

// InputField represents a single input field definition
type InputField struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// ToAIToolsSchema converts InputsSchema to aitools.Schema
func (s *InputsSchema) ToAIToolsSchema() aitools.Schema {
	if s == nil {
		return aitools.Schema{
			Type:       aitools.TypeObject,
			Properties: make(aitools.PropertyMap),
		}
	}

	props := make(aitools.PropertyMap)
	var required []string

	for _, field := range s.Fields {
		props[field.Name] = aitools.Property{
			Type:        stringToPropertyType(field.Type),
			Description: field.Description,
		}
		if field.Required {
			required = append(required, field.Name)
		}
	}

	return aitools.Schema{
		Type:       aitools.TypeObject,
		Properties: props,
		Required:   required,
	}
}

func stringToPropertyType(s string) aitools.PropertyType {
	switch s {
	case "string":
		return aitools.TypeString
	case "number":
		return aitools.TypeNumber
	case "integer":
		return aitools.TypeInteger
	case "boolean":
		return aitools.TypeBoolean
	case "array":
		return aitools.TypeArray
	case "object":
		return aitools.TypeObject
	default:
		return aitools.TypeString
	}
}

// Validate checks that the custom tool configuration is valid
func (t *CustomTool) Validate() error {
	if t.Implements == "" {
		return fmt.Errorf("tool '%s': implements is required", t.Name)
	}

	// All implements values must be in plugins.{namespace}.{tool} format
	if !t.IsPluginTool() {
		return fmt.Errorf("tool '%s': implements must be in plugins.{namespace}.{tool} format, got '%s'", t.Name, t.Implements)
	}

	return nil
}

// IsPluginTool returns true if this tool implements a plugin tool
func (t *CustomTool) IsPluginTool() bool {
	return strings.HasPrefix(t.Implements, "plugins.")
}

// GetPluginToolRef returns the plugin name and tool name if this is a plugin tool
func (t *CustomTool) GetPluginToolRef() (pluginName, toolName string, ok bool) {
	if !t.IsPluginTool() {
		return "", "", false
	}
	parts := strings.Split(t.Implements, ".")
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// GetImplementedTool returns an instance of the implemented tool (internal plugin tools only)
// Handles plugins.bash.bash, plugins.http.get, etc.
func (t *CustomTool) GetImplementedTool() aitools.Tool {
	if !IsInternalPluginTool(t.Implements) {
		return nil
	}

	switch t.Implements {
	case "plugins.bash.bash":
		return &aitools.BashTool{}
	case "plugins.http.get":
		return &aitools.HTTPGetTool{}
	case "plugins.http.post":
		return &aitools.HTTPPostTool{}
	case "plugins.http.put":
		return &aitools.HTTPPutTool{}
	case "plugins.http.patch":
		return &aitools.HTTPPatchTool{}
	case "plugins.http.delete":
		return &aitools.HTTPDeleteTool{}
	default:
		return nil
	}
}

// GetImplementedToolWithPlugins returns an instance of the implemented tool, including external plugin tools
func (t *CustomTool) GetImplementedToolWithPlugins(loadedPlugins map[string]*plugin.PluginClient) aitools.Tool {
	// Try internal plugin tool first (plugins.bash.bash, plugins.http.get)
	if tool := t.GetImplementedTool(); tool != nil {
		return tool
	}

	// Try external plugin tool
	pluginName, toolName, ok := t.GetPluginToolRef()
	if !ok {
		return nil
	}

	client, ok := loadedPlugins[pluginName]
	if !ok {
		return nil
	}

	tool, err := client.GetTool(toolName)
	if err != nil {
		return nil
	}

	return tool
}

// GetImplementedToolSchema returns the schema of the implemented tool
func (t *CustomTool) GetImplementedToolSchema() aitools.Schema {
	tool := t.GetImplementedTool()
	if tool == nil {
		return aitools.Schema{}
	}
	return tool.ToolPayloadSchema()
}

// BuildInputsCtyType returns the cty type for the inputs namespace
func (t *CustomTool) BuildInputsCtyType() cty.Type {
	if t.Inputs == nil {
		return cty.EmptyObject
	}

	attrTypes := make(map[string]cty.Type)
	for _, field := range t.Inputs.Fields {
		attrTypes[field.Name] = stringToCtyType(field.Type)
	}
	return cty.Object(attrTypes)
}

func stringToCtyType(s string) cty.Type {
	switch s {
	case "string":
		return cty.String
	case "number", "integer":
		return cty.Number
	case "boolean":
		return cty.Bool
	case "array":
		return cty.List(cty.DynamicPseudoType)
	case "object":
		return cty.DynamicPseudoType
	default:
		return cty.String
	}
}

// BuildFieldsEvalContext creates an HCL eval context with the inputs placeholder
// This is used when parsing the tool's dynamic fields
func BuildFieldsEvalContext(baseCtx *hcl.EvalContext, inputsType cty.Type) *hcl.EvalContext {
	// Create placeholder values for inputs based on the type
	inputsPlaceholder := cty.UnknownVal(inputsType)

	newVars := make(map[string]cty.Value)
	if baseCtx != nil {
		for k, v := range baseCtx.Variables {
			newVars[k] = v
		}
	}
	newVars["inputs"] = inputsPlaceholder

	return &hcl.EvalContext{
		Variables: newVars,
	}
}

// ToTool creates an aitools.Tool from this custom tool configuration (internal tools only)
func (t *CustomTool) ToTool() aitools.Tool {
	baseTool := t.GetImplementedTool()
	if baseTool == nil {
		return nil
	}

	inputSchema := t.Inputs.ToAIToolsSchema()

	return &customToolRuntime{
		name:        t.Name,
		description: t.Description,
		baseTool:    baseTool,
		inputSchema: inputSchema,
		fieldExprs:  t.FieldExprs,
	}
}

// ToToolWithPlugins creates an aitools.Tool from this custom tool configuration, supporting plugin tools
func (t *CustomTool) ToToolWithPlugins(loadedPlugins map[string]*plugin.PluginClient) aitools.Tool {
	baseTool := t.GetImplementedToolWithPlugins(loadedPlugins)
	if baseTool == nil {
		return nil
	}

	inputSchema := t.Inputs.ToAIToolsSchema()

	return &customToolRuntime{
		name:        t.Name,
		description: t.Description,
		baseTool:    baseTool,
		inputSchema: inputSchema,
		fieldExprs:  t.FieldExprs,
	}
}

// customToolRuntime implements aitools.Tool and evaluates expressions at runtime
type customToolRuntime struct {
	name        string
	description string
	baseTool    aitools.Tool
	inputSchema aitools.Schema
	fieldExprs  map[string]hcl.Expression
}

func (t *customToolRuntime) ToolName() string {
	return t.name
}

func (t *customToolRuntime) ToolDescription() string {
	if t.description != "" {
		return t.description
	}
	return t.baseTool.ToolDescription()
}

func (t *customToolRuntime) ToolPayloadSchema() aitools.Schema {
	return t.inputSchema
}

func (t *customToolRuntime) Call(params string) string {
	// Parse the incoming inputs
	var inputValues map[string]any
	if err := json.Unmarshal([]byte(params), &inputValues); err != nil {
		return "Error: invalid input parameters - " + err.Error()
	}

	// Build eval context with actual input values
	inputsCty := mapToCtyValue(inputValues)
	evalCtx := &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"inputs": inputsCty,
		},
	}

	// Evaluate each field expression with actual inputs
	baseParams := make(map[string]any)
	for fieldName, expr := range t.fieldExprs {
		val, diags := expr.Value(evalCtx)
		if diags.HasErrors() {
			return fmt.Sprintf("Error: failed to evaluate field '%s' - %s", fieldName, diags.Error())
		}
		baseParams[fieldName] = ctyValueToGo(val)
	}

	// Marshal to JSON for the base tool
	baseParamsJSON, err := json.Marshal(baseParams)
	if err != nil {
		return "Error: failed to marshal base parameters - " + err.Error()
	}

	return t.baseTool.Call(string(baseParamsJSON))
}

// mapToCtyValue converts a Go map to cty.Value
func mapToCtyValue(m map[string]any) cty.Value {
	if len(m) == 0 {
		return cty.EmptyObjectVal
	}

	vals := make(map[string]cty.Value)
	for k, v := range m {
		vals[k] = goToCtyValue(v)
	}
	return cty.ObjectVal(vals)
}

// goToCtyValue converts a Go value to cty.Value
func goToCtyValue(v any) cty.Value {
	switch val := v.(type) {
	case string:
		return cty.StringVal(val)
	case float64:
		return cty.NumberFloatVal(val)
	case int:
		return cty.NumberIntVal(int64(val))
	case bool:
		return cty.BoolVal(val)
	case map[string]any:
		return mapToCtyValue(val)
	case []any:
		if len(val) == 0 {
			return cty.ListValEmpty(cty.DynamicPseudoType)
		}
		vals := make([]cty.Value, len(val))
		for i, item := range val {
			vals[i] = goToCtyValue(item)
		}
		return cty.TupleVal(vals)
	case nil:
		return cty.NullVal(cty.DynamicPseudoType)
	default:
		return cty.StringVal(fmt.Sprintf("%v", v))
	}
}

// ctyValueToGo converts a cty.Value to a Go value
func ctyValueToGo(val cty.Value) any {
	if val.IsNull() || !val.IsKnown() {
		return nil
	}

	switch {
	case val.Type() == cty.String:
		return val.AsString()
	case val.Type() == cty.Number:
		f, _ := val.AsBigFloat().Float64()
		return f
	case val.Type() == cty.Bool:
		return val.True()
	case val.Type().IsObjectType() || val.Type().IsMapType():
		result := make(map[string]any)
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			result[k.AsString()] = ctyValueToGo(v)
		}
		return result
	case val.Type().IsTupleType() || val.Type().IsListType():
		var result []any
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			result = append(result, ctyValueToGo(v))
		}
		return result
	default:
		return nil
	}
}
