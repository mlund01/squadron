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
}

// NewDatasetCursor creates a new cursor for the given items
func NewDatasetCursor(taskName string, items []cty.Value) *DatasetCursor {
	return &DatasetCursor{
		items:    items,
		index:    0,
		taskName: taskName,
	}
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
	// OutputCounter returns the number of outputs submitted so far.
	// Used to gate advancement: must submit output before getting next item.
	OutputCounter func() int
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

You MUST call submit_output after processing each item before calling dataset_next again.`
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

	// Validate that previous item's output was submitted (if any)
	if t.cursor.index > 0 && t.OutputCounter != nil {
		if t.OutputCounter() < t.cursor.index {
			return `{"status": "error", "message": "must call submit_output for current item before getting next item"}`
		}
	}

	// Check if exhausted
	if t.cursor.index >= len(t.cursor.items) {
		submitted := 0
		if t.OutputCounter != nil {
			submitted = t.OutputCounter()
		}
		return fmt.Sprintf(`{"status": "exhausted", "message": "No more items in dataset", "completed": %d}`, submitted)
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
