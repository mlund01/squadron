package aitools

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zclconf/go-cty/cty"
)

// DatasetCursor tracks position in a sequential dataset iteration
type DatasetCursor struct {
	items    []cty.Value
	index    int
	taskName string
	mu       sync.Mutex

	// Results collected via dataset_item_complete
	results []DatasetItemResult
}

// DatasetItemResult holds the output from one dataset item
type DatasetItemResult struct {
	Index   int
	Output  map[string]any
	Summary string
	Success bool
}

// NewDatasetCursor creates a new cursor for the given items
func NewDatasetCursor(taskName string, items []cty.Value) *DatasetCursor {
	return &DatasetCursor{
		items:    items,
		index:    0,
		taskName: taskName,
		results:  make([]DatasetItemResult, 0, len(items)),
	}
}

// GetResults returns all collected results
func (c *DatasetCursor) GetResults() []DatasetItemResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.results
}

// Total returns the total number of items
func (c *DatasetCursor) Total() int {
	return len(c.items)
}

// =============================================================================
// DatasetNextTool - advances to the next item in the dataset
// =============================================================================

type DatasetNextTool struct {
	cursor *DatasetCursor
}

func NewDatasetNextTool(cursor *DatasetCursor) *DatasetNextTool {
	return &DatasetNextTool{cursor: cursor}
}

func (t *DatasetNextTool) ToolName() string {
	return "dataset_next"
}

func (t *DatasetNextTool) ToolDescription() string {
	return `Get the next item from the dataset for sequential processing.

Returns:
- {"status": "ok", "index": N, "total": M, "item": {...}} - The next item to process
- {"status": "exhausted", "message": "..."} - No more items in dataset

You MUST call dataset_item_complete after processing each item before calling dataset_next again.`
}

func (t *DatasetNextTool) ToolPayloadSchema() Schema {
	return Schema{
		Type:       TypeObject,
		Properties: PropertyMap{}, // No parameters needed
	}
}

func (t *DatasetNextTool) Call(params string) string {
	t.cursor.mu.Lock()
	defer t.cursor.mu.Unlock()

	// Validate that previous item was completed (if any)
	if t.cursor.index > 0 {
		expectedResults := t.cursor.index
		if len(t.cursor.results) < expectedResults {
			return `{"status": "error", "message": "must call dataset_item_complete for current item before getting next item"}`
		}
	}

	// Check if exhausted
	if t.cursor.index >= len(t.cursor.items) {
		return fmt.Sprintf(`{"status": "exhausted", "message": "No more items in dataset", "completed": %d}`, len(t.cursor.results))
	}

	// Get current item and advance
	item := t.cursor.items[t.cursor.index]
	currentIndex := t.cursor.index
	t.cursor.index++

	// Convert cty.Value to JSON
	itemGo := ctyValueToGo(item)
	itemJSON, err := json.Marshal(itemGo)
	if err != nil {
		return fmt.Sprintf(`{"status": "error", "message": "failed to serialize item: %v"}`, err)
	}

	return fmt.Sprintf(`{"status": "ok", "index": %d, "total": %d, "item": %s}`,
		currentIndex, len(t.cursor.items), string(itemJSON))
}

// =============================================================================
// DatasetItemCompleteTool - records output for the current item
// =============================================================================

type DatasetItemCompleteTool struct {
	cursor *DatasetCursor
}

func NewDatasetItemCompleteTool(cursor *DatasetCursor) *DatasetItemCompleteTool {
	return &DatasetItemCompleteTool{cursor: cursor}
}

func (t *DatasetItemCompleteTool) ToolName() string {
	return "dataset_item_complete"
}

func (t *DatasetItemCompleteTool) ToolDescription() string {
	return `Submit the output for the current dataset item. This MUST be called after processing each item and before calling dataset_next for the next item.

The output should contain the structured result of processing this item.`
}

func (t *DatasetItemCompleteTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"output": {
				Type:        TypeObject,
				Description: "The structured output for this item (should match task output schema if defined)",
			},
			"summary": {
				Type:        TypeString,
				Description: "Brief summary of what was accomplished for this item",
			},
		},
		Required: []string{"output"},
	}
}

func (t *DatasetItemCompleteTool) Call(params string) string {
	var input struct {
		Output  map[string]any `json:"output"`
		Summary string         `json:"summary"`
	}
	if err := json.Unmarshal([]byte(params), &input); err != nil {
		return fmt.Sprintf(`{"status": "error", "message": "invalid input: %v"}`, err)
	}

	t.cursor.mu.Lock()
	defer t.cursor.mu.Unlock()

	// Current item index is cursor.index - 1 (since dataset_next increments before returning)
	currentIndex := t.cursor.index - 1
	if currentIndex < 0 {
		return `{"status": "error", "message": "no current item - call dataset_next first"}`
	}

	// Check if this item was already completed
	for _, r := range t.cursor.results {
		if r.Index == currentIndex {
			return fmt.Sprintf(`{"status": "error", "message": "item %d already completed"}`, currentIndex)
		}
	}

	// Record the result
	result := DatasetItemResult{
		Index:   currentIndex,
		Output:  input.Output,
		Summary: input.Summary,
		Success: true,
	}
	t.cursor.results = append(t.cursor.results, result)

	remaining := len(t.cursor.items) - t.cursor.index
	return fmt.Sprintf(`{"status": "ok", "recorded_index": %d, "items_remaining": %d}`, currentIndex, remaining)
}
