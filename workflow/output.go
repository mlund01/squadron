package workflow

import (
	"time"
)

// TaskOutput is the unified output structure for all tasks
type TaskOutput struct {
	// Standard fields (always present)
	TaskName  string    `json:"task_name"`
	Status    string    `json:"status"` // "success" or "failed"
	Summary   string    `json:"summary"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`

	// Structured output (if schema defined)
	Output map[string]any `json:"output,omitempty"`

	// For iterated tasks
	IsIterated      bool              `json:"is_iterated,omitempty"`
	TotalIterations int               `json:"total_iterations,omitempty"`
	Iterations      []IterationOutput `json:"iterations,omitempty"`
}

// IterationOutput is the output for a single iteration
type IterationOutput struct {
	Index     int            `json:"index"`
	ItemID    string         `json:"item_id"`
	Status    string         `json:"status"`
	Summary   string         `json:"summary"`
	Output    map[string]any `json:"output,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}
