package aitools

import (
	"encoding/json"
	"fmt"
)

// TaskCompleteTool allows the commander to signal that it has finished the task.
type TaskCompleteTool struct {
	completed     bool
	succeeded     bool
	failureReason string
	// SubtaskChecker returns (total, incomplete) subtask counts.
	// If set, task_complete will fail when incomplete > 0.
	SubtaskChecker func() (total int, incomplete int)
}

func (t *TaskCompleteTool) ToolName() string {
	return "task_complete"
}

func (t *TaskCompleteTool) ToolDescription() string {
	return "Signal that you have completed the task. Call this when all work is done and all subtasks are complete. Pass succeed=false with a reason if the task cannot be completed successfully."
}

func (t *TaskCompleteTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: map[string]Property{
			"succeed": {
				Type:        TypeBoolean,
				Description: "Whether the task completed successfully. Defaults to true. Set to false to mark the task as failed.",
			},
			"reason": {
				Type:        TypeString,
				Description: "Explanation of why the task failed. Required when succeed=false.",
			},
		},
	}
}

func (t *TaskCompleteTool) Call(params string) string {
	// Parse params (succeed defaults to true)
	succeed := true
	reason := ""
	if params != "" && params != "{}" {
		var input struct {
			Succeed *bool  `json:"succeed"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(params), &input); err == nil {
			if input.Succeed != nil {
				succeed = *input.Succeed
			}
			reason = input.Reason
		}
	}

	// Require reason when failing
	if !succeed && reason == "" {
		return `{"status": "error", "error": "A reason is required when marking a task as failed. Call task_complete with succeed=false and a reason explaining why."}`
	}

	// Only check subtasks when succeeding
	if succeed {
		if t.SubtaskChecker != nil {
			total, incomplete := t.SubtaskChecker()
			if total > 0 && incomplete > 0 {
				return fmt.Sprintf(`{"status": "error", "error": "Cannot complete task: %d of %d subtasks are not yet complete. Call complete_subtask for each remaining subtask before calling task_complete."}`, incomplete, total)
			}
		}
	}

	t.completed = true
	t.succeeded = succeed
	if !succeed {
		t.failureReason = reason
	}

	if !succeed {
		return `{"status": "ok", "message": "Task marked as failed."}`
	}
	return `{"status": "ok"}`
}

// IsCompleted returns whether the tool has been called.
func (t *TaskCompleteTool) IsCompleted() bool {
	return t.completed
}

// IsSucceeded returns whether the task completed successfully.
func (t *TaskCompleteTool) IsSucceeded() bool {
	return t.succeeded
}

// FailureReason returns the reason provided when the task was marked as failed.
func (t *TaskCompleteTool) FailureReason() string {
	return t.failureReason
}
