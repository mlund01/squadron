package aitools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"
)

// =============================================================================
// ResultInfoTool - returns info about a stored result
// =============================================================================

// ResultInfoTool returns info about a stored result
type ResultInfoTool struct {
	Store ResultStore
}

func (t *ResultInfoTool) ToolName() string {
	return "result_info"
}

func (t *ResultInfoTool) ToolDescription() string {
	return "Get info about a stored large result (type, size, ID)"
}

func (t *ResultInfoTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"id": {
				Type:        TypeString,
				Description: "The result ID (e.g. _result_http_get_1)",
			},
		},
		Required: []string{"id"},
	}
}

func (t *ResultInfoTool) Call(params string) string {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(params), &args); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	r, ok := t.Store.Get(args.ID)
	if !ok {
		return fmt.Sprintf("Error: result '%s' not found", args.ID)
	}

	return fmt.Sprintf("ID: %s\nType: %s\nSize: %d", r.ID, r.Type, r.Size)
}

// =============================================================================
// ResultItemsTool - retrieves items from an array result
// =============================================================================

// ResultItemsTool retrieves items from an array result
type ResultItemsTool struct {
	Store ResultStore
}

func (t *ResultItemsTool) ToolName() string {
	return "result_items"
}

func (t *ResultItemsTool) ToolDescription() string {
	return "Get items from a large array result by offset and count"
}

func (t *ResultItemsTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"id": {
				Type:        TypeString,
				Description: "The result ID (e.g. _result_http_get_1)",
			},
			"offset": {
				Type:        TypeInteger,
				Description: "Starting index (0-based)",
			},
			"count": {
				Type:        TypeInteger,
				Description: "Number of items to return",
			},
		},
		Required: []string{"id", "offset", "count"},
	}
}

func (t *ResultItemsTool) Call(params string) string {
	var args struct {
		ID     string `json:"id"`
		Offset int    `json:"offset"`
		Count  int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(params), &args); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	r, ok := t.Store.Get(args.ID)
	if !ok {
		return fmt.Sprintf("Error: result '%s' not found", args.ID)
	}
	if r.Type != ResultTypeArray {
		return fmt.Sprintf("Error: result '%s' is not an array (type: %s)", args.ID, r.Type)
	}

	end := args.Offset + args.Count
	if end > len(r.Array) {
		end = len(r.Array)
	}
	if args.Offset >= len(r.Array) {
		return "[]"
	}

	items := r.Array[args.Offset:end]
	out, _ := json.MarshalIndent(items, "", "  ")
	return string(out)
}

// =============================================================================
// ResultGetTool - retrieves a value from an object using dot notation
// =============================================================================

// ResultGetTool retrieves a value from an object result using dot path
type ResultGetTool struct {
	Store ResultStore
}

func (t *ResultGetTool) ToolName() string {
	return "result_get"
}

func (t *ResultGetTool) ToolDescription() string {
	return "Get a value from an object or array result using dot path notation (e.g. 'users.0.name')"
}

func (t *ResultGetTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"id": {
				Type:        TypeString,
				Description: "The result ID",
			},
			"path": {
				Type:        TypeString,
				Description: "Dot-notation path to the value (e.g. 'settings.theme' or 'users.0.name')",
			},
		},
		Required: []string{"id", "path"},
	}
}

func (t *ResultGetTool) Call(params string) string {
	var args struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(params), &args); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	r, ok := t.Store.Get(args.ID)
	if !ok {
		return fmt.Sprintf("Error: result '%s' not found", args.ID)
	}
	if r.Type != ResultTypeObject && r.Type != ResultTypeArray {
		return fmt.Sprintf("Error: result '%s' is not an object or array (type: %s)", args.ID, r.Type)
	}

	// Navigate path (e.g., "users.0.name")
	parts := strings.Split(args.Path, ".")
	var current any = r.Object
	if r.Type == ResultTypeArray {
		current = r.Array
	}

	for _, part := range parts {
		if part == "" {
			continue
		}
		switch v := current.(type) {
		case map[string]any:
			var exists bool
			current, exists = v[part]
			if !exists {
				return fmt.Sprintf("Error: key '%s' not found", part)
			}
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Sprintf("Error: '%s' is not a valid array index", part)
			}
			if idx < 0 || idx >= len(v) {
				return fmt.Sprintf("Error: index %d out of range (array has %d items)", idx, len(v))
			}
			current = v[idx]
		default:
			return fmt.Sprintf("Error: cannot navigate into %T at '%s'", current, part)
		}
	}

	out, _ := json.MarshalIndent(current, "", "  ")
	return string(out)
}

// =============================================================================
// ResultKeysTool - returns keys of an object result
// =============================================================================

// ResultKeysTool returns keys of an object result
type ResultKeysTool struct {
	Store ResultStore
}

func (t *ResultKeysTool) ToolName() string {
	return "result_keys"
}

func (t *ResultKeysTool) ToolDescription() string {
	return "Get the keys of an object result, optionally at a nested path"
}

func (t *ResultKeysTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"id": {
				Type:        TypeString,
				Description: "The result ID",
			},
			"path": {
				Type:        TypeString,
				Description: "Optional dot-notation path to a nested object",
			},
		},
		Required: []string{"id"},
	}
}

func (t *ResultKeysTool) Call(params string) string {
	var args struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(params), &args); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	r, ok := t.Store.Get(args.ID)
	if !ok {
		return fmt.Sprintf("Error: result '%s' not found", args.ID)
	}
	if r.Type != ResultTypeObject {
		return fmt.Sprintf("Error: result '%s' is not an object (type: %s)", args.ID, r.Type)
	}

	// Navigate to nested object if path provided
	var target map[string]any = r.Object
	if args.Path != "" {
		parts := strings.Split(args.Path, ".")
		var current any = r.Object

		for _, part := range parts {
			if part == "" {
				continue
			}
			switch v := current.(type) {
			case map[string]any:
				var exists bool
				current, exists = v[part]
				if !exists {
					return fmt.Sprintf("Error: key '%s' not found", part)
				}
			case []any:
				idx, err := strconv.Atoi(part)
				if err != nil {
					return fmt.Sprintf("Error: '%s' is not a valid array index", part)
				}
				if idx < 0 || idx >= len(v) {
					return fmt.Sprintf("Error: index %d out of range", idx)
				}
				current = v[idx]
			default:
				return fmt.Sprintf("Error: cannot navigate into %T", current)
			}
		}

		obj, ok := current.(map[string]any)
		if !ok {
			return fmt.Sprintf("Error: value at path is not an object (type: %T)", current)
		}
		target = obj
	}

	keys := make([]string, 0, len(target))
	for k := range target {
		keys = append(keys, k)
	}
	out, _ := json.Marshal(keys)
	return string(out)
}

// =============================================================================
// ResultChunkTool - retrieves a chunk of text from a text result
// =============================================================================

// ResultChunkTool retrieves a chunk of text from a text result
type ResultChunkTool struct {
	Store ResultStore
}

func (t *ResultChunkTool) ToolName() string {
	return "result_chunk"
}

func (t *ResultChunkTool) ToolDescription() string {
	return "Get a chunk of text from a large text result by offset and length"
}

func (t *ResultChunkTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"id": {
				Type:        TypeString,
				Description: "The result ID",
			},
			"offset": {
				Type:        TypeInteger,
				Description: "Starting byte offset",
			},
			"length": {
				Type:        TypeInteger,
				Description: "Number of bytes to return",
			},
		},
		Required: []string{"id", "offset", "length"},
	}
}

func (t *ResultChunkTool) Call(params string) string {
	var args struct {
		ID     string `json:"id"`
		Offset int    `json:"offset"`
		Length int    `json:"length"`
	}
	if err := json.Unmarshal([]byte(params), &args); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	r, ok := t.Store.Get(args.ID)
	if !ok {
		return fmt.Sprintf("Error: result '%s' not found", args.ID)
	}

	text := r.RawData
	if args.Offset >= len(text) {
		return ""
	}
	end := args.Offset + args.Length
	if end > len(text) {
		end = len(text)
	}

	return text[args.Offset:end]
}

// =============================================================================
// ResultToDatasetTool - promotes an array result to the DatasetStore
// =============================================================================

// ResultToDatasetTool promotes an array result to the DatasetStore
type ResultToDatasetTool struct {
	ResultStore  ResultStore
	DatasetStore DatasetStore
}

func (t *ResultToDatasetTool) ToolName() string {
	return "result_to_dataset"
}

func (t *ResultToDatasetTool) ToolDescription() string {
	return "Store a large array result as a dataset for iteration and processing with dataset tools"
}

func (t *ResultToDatasetTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"id": {
				Type:        TypeString,
				Description: "The result ID (e.g. _result_http_get_1)",
			},
			"dataset_name": {
				Type:        TypeString,
				Description: "Name for the new dataset",
			},
		},
		Required: []string{"id", "dataset_name"},
	}
}

func (t *ResultToDatasetTool) Call(params string) string {
	var args struct {
		ID          string `json:"id"`
		DatasetName string `json:"dataset_name"`
	}
	if err := json.Unmarshal([]byte(params), &args); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	if t.DatasetStore == nil {
		return "Error: dataset tools are only available within mission context"
	}

	// Get the result from ResultStore
	r, ok := t.ResultStore.Get(args.ID)
	if !ok {
		return fmt.Sprintf("Error: result '%s' not found", args.ID)
	}

	// Only arrays can be promoted to datasets
	if r.Type != ResultTypeArray {
		return fmt.Sprintf("Error: only array results can be stored as datasets. Result '%s' is type '%s'", args.ID, r.Type)
	}

	// Convert []any to []cty.Value
	ctyItems := make([]cty.Value, len(r.Array))
	for i, item := range r.Array {
		ctyItems[i] = goToCtyValue(item)
	}

	// Store in DatasetStore
	if err := t.DatasetStore.SetDataset(args.DatasetName, ctyItems); err != nil {
		return fmt.Sprintf("Error: failed to create dataset: %v", err)
	}

	return fmt.Sprintf("Created dataset '%s' with %d items. Use dataset_sample(\"%s\", N) or iterate with for_each.",
		args.DatasetName, len(r.Array), args.DatasetName)
}
