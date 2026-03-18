package aitools

import (
	"encoding/json"
	"fmt"
	"sync"
)

// OutputField describes a required or optional output field for validation
type OutputField struct {
	Name     string
	Type     string
	Required bool
}

// SubmitResult holds one submitted output
type SubmitResult struct {
	Output map[string]any
}

// SubmitOutputCallback is called after each output submission
type SubmitOutputCallback func(index int, output map[string]any)

// SubmitOutputTool allows the LLM to submit structured task output.
// Used by all task types: non-iterated, sequential iterations, and parallel iterations.
type SubmitOutputTool struct {
	schema   []OutputField
	OnSubmit SubmitOutputCallback
	results  []SubmitResult
	mu       sync.Mutex
}

// NewSubmitOutputTool creates a new submit_output tool with optional schema validation
func NewSubmitOutputTool(schema []OutputField) *SubmitOutputTool {
	return &SubmitOutputTool{
		schema:  schema,
		results: make([]SubmitResult, 0),
	}
}

func (t *SubmitOutputTool) ToolName() string {
	return "submit_output"
}

func (t *SubmitOutputTool) ToolDescription() string {
	return `Submit the structured output for the current task. You MUST call this tool to deliver your results.

Parameters:
- output: A JSON object containing the structured result of your work. Must include all required fields defined in the task output schema.

Call this tool once when you have completed your task. For sequential dataset processing, call it once per item after processing each item.`
}

func (t *SubmitOutputTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"output": {
				Type:        TypeObject,
				Description: "The structured output (must match the task output schema if defined)",
			},
		},
		Required: []string{"output"},
	}
}

func (t *SubmitOutputTool) Call(params string) string {
	var input struct {
		Output map[string]any `json:"output"`
	}
	if err := json.Unmarshal([]byte(params), &input); err != nil {
		return fmt.Sprintf(`{"status": "error", "message": "invalid input: %v"}`, err)
	}

	if input.Output == nil {
		return `{"status": "error", "message": "output is required and must be a JSON object"}`
	}

	// Validate against schema if provided
	if len(t.schema) > 0 {
		var missing []string
		for _, field := range t.schema {
			if !field.Required {
				continue
			}
			val, exists := input.Output[field.Name]
			if !exists || val == nil {
				missing = append(missing, field.Name)
			}
		}
		if len(missing) > 0 {
			return fmt.Sprintf(`{"status": "error", "message": "missing required output fields: %v"}`, missing)
		}
	}

	t.mu.Lock()
	index := len(t.results)
	t.results = append(t.results, SubmitResult{
		Output: input.Output,
	})
	t.mu.Unlock()

	// Fire callback for persistence
	if t.OnSubmit != nil {
		t.OnSubmit(index, input.Output)
	}

	return fmt.Sprintf(`{"status": "ok", "index": %d}`, index)
}

// ResultCount returns the number of outputs submitted so far
func (t *SubmitOutputTool) ResultCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.results)
}

// GetResults returns all submitted outputs
func (t *SubmitOutputTool) GetResults() []SubmitResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SubmitResult, len(t.results))
	copy(out, t.results)
	return out
}
