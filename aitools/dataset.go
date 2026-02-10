package aitools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"
)

// DatasetStore provides access to mission datasets at runtime
type DatasetStore interface {
	// SetDataset sets a dataset's values
	SetDataset(name string, items []cty.Value) error
	// GetDatasetSample returns a sample of items from a dataset
	GetDatasetSample(name string, count int) ([]cty.Value, error)
	// GetDatasetCount returns the number of items in a dataset
	GetDatasetCount(name string) (int, error)
	// GetDatasetInfo returns information about all available datasets
	GetDatasetInfo() []DatasetInfo
}

// DatasetInfo describes a dataset
type DatasetInfo struct {
	Name        string
	Description string
	Schema      []FieldInfo // nil if no schema
	ItemCount   int
}

// FieldInfo describes a field in a dataset schema
type FieldInfo struct {
	Name     string
	Type     string
	Required bool
}

// =============================================================================
// SetDatasetTool - sets a dataset's values
// =============================================================================

// SetDatasetTool allows agents to set a dataset's values at runtime
type SetDatasetTool struct {
	Store DatasetStore
}

func (t *SetDatasetTool) ToolName() string {
	return "set_dataset"
}

func (t *SetDatasetTool) ToolDescription() string {
	var sb strings.Builder
	sb.WriteString("Set a dataset's values. Use this to populate a dataset with items for subsequent tasks to iterate over.\n\n")

	if t.Store != nil {
		info := t.Store.GetDatasetInfo()
		if len(info) > 0 {
			sb.WriteString("**Available datasets:**\n")
			for _, ds := range info {
				sb.WriteString(fmt.Sprintf("- **%s**", ds.Name))
				if ds.Description != "" {
					sb.WriteString(fmt.Sprintf(": %s", ds.Description))
				}
				sb.WriteString("\n")
				if len(ds.Schema) > 0 {
					sb.WriteString("  Schema:\n")
					for _, field := range ds.Schema {
						req := ""
						if field.Required {
							req = " (required)"
						}
						sb.WriteString(fmt.Sprintf("    - %s: %s%s\n", field.Name, field.Type, req))
					}
				}
			}
		}
	}

	return sb.String()
}

func (t *SetDatasetTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"name": {
				Type:        TypeString,
				Description: "The name of the dataset to set",
			},
			"items": {
				Type:        TypeArray,
				Description: "The list of items to set in the dataset. Each item should match the dataset's schema.",
			},
		},
		Required: []string{"name", "items"},
	}
}

func (t *SetDatasetTool) Call(params string) string {
	if t.Store == nil {
		return "Error: dataset tools are only available within mission context"
	}

	var input struct {
		Name  string `json:"name"`
		Items []any  `json:"items"`
	}
	if err := json.Unmarshal([]byte(params), &input); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	// Convert []any to []cty.Value
	ctyItems := make([]cty.Value, len(input.Items))
	for i, item := range input.Items {
		ctyItems[i] = goToCtyValue(item)
	}

	if err := t.Store.SetDataset(input.Name, ctyItems); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return fmt.Sprintf("Successfully set dataset '%s' with %d items", input.Name, len(ctyItems))
}

// =============================================================================
// DatasetSampleTool - gets sample items from a dataset
// =============================================================================

// DatasetSampleTool allows agents to get sample items from a dataset
type DatasetSampleTool struct {
	Store DatasetStore
}

func (t *DatasetSampleTool) ToolName() string {
	return "dataset_sample"
}

func (t *DatasetSampleTool) ToolDescription() string {
	var sb strings.Builder
	sb.WriteString("Get sample items from a dataset. Use this to inspect the contents of a dataset.\n\n")

	if t.Store != nil {
		info := t.Store.GetDatasetInfo()
		if len(info) > 0 {
			sb.WriteString("**Available datasets:**\n")
			for _, ds := range info {
				sb.WriteString(fmt.Sprintf("- **%s** (%d items)", ds.Name, ds.ItemCount))
				if ds.Description != "" {
					sb.WriteString(fmt.Sprintf(": %s", ds.Description))
				}
				sb.WriteString("\n")
			}
		}
	}

	return sb.String()
}

func (t *DatasetSampleTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"name": {
				Type:        TypeString,
				Description: "The name of the dataset to sample from",
			},
			"count": {
				Type:        TypeInteger,
				Description: "The number of items to return (default: 5)",
			},
		},
		Required: []string{"name"},
	}
}

func (t *DatasetSampleTool) Call(params string) string {
	if t.Store == nil {
		return "Error: dataset tools are only available within mission context"
	}

	var input struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(params), &input); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	// Default count to 5
	if input.Count <= 0 {
		input.Count = 5
	}

	items, err := t.Store.GetDatasetSample(input.Name, input.Count)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	// Convert to Go values for JSON output
	goItems := make([]any, len(items))
	for i, item := range items {
		goItems[i] = ctyValueToGo(item)
	}

	result, err := json.MarshalIndent(goItems, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling result: %v", err)
	}

	return string(result)
}

// =============================================================================
// DatasetCountTool - gets the count of items in a dataset
// =============================================================================

// DatasetCountTool allows agents to get the count of items in a dataset
type DatasetCountTool struct {
	Store DatasetStore
}

func (t *DatasetCountTool) ToolName() string {
	return "dataset_count"
}

func (t *DatasetCountTool) ToolDescription() string {
	var sb strings.Builder
	sb.WriteString("Get the number of items in a dataset.\n\n")

	if t.Store != nil {
		info := t.Store.GetDatasetInfo()
		if len(info) > 0 {
			sb.WriteString("**Available datasets:**\n")
			for _, ds := range info {
				sb.WriteString(fmt.Sprintf("- **%s** (%d items)\n", ds.Name, ds.ItemCount))
			}
		}
	}

	return sb.String()
}

func (t *DatasetCountTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"name": {
				Type:        TypeString,
				Description: "The name of the dataset",
			},
		},
		Required: []string{"name"},
	}
}

func (t *DatasetCountTool) Call(params string) string {
	if t.Store == nil {
		return "Error: dataset tools are only available within mission context"
	}

	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(params), &input); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	count, err := t.Store.GetDatasetCount(input.Name)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return fmt.Sprintf("%d", count)
}

// =============================================================================
// Helper functions for cty conversion (duplicated from config/tool.go to avoid
// circular imports - these are simple conversions)
// =============================================================================

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
		if len(val) == 0 {
			return cty.EmptyObjectVal
		}
		vals := make(map[string]cty.Value)
		for k, v := range val {
			vals[k] = goToCtyValue(v)
		}
		return cty.ObjectVal(vals)
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
