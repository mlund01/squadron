package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"squadron/aitools"
	"squadron/store"
)

// setSubtasksTool allows the commander to define subtasks for the current task
type setSubtasksTool struct {
	onSet func(titles []string) error
	onGet func() ([]store.Subtask, error)
}

func (t *setSubtasksTool) ToolName() string { return "set_subtasks" }

func (t *setSubtasksTool) ToolDescription() string {
	return `Define the ordered subtasks for this task. This MUST be your first action. Provide 1-10 subtask titles as an ordered list. Cannot be called again once any subtask has been completed.`
}

func (t *setSubtasksTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"subtasks": {
				Type:        aitools.TypeArray,
				Description: "Ordered list of subtask titles (1-10)",
				Items:       &aitools.Property{Type: aitools.TypeString},
			},
		},
		Required: []string{"subtasks"},
	}
}

func (t *setSubtasksTool) Call(params string) string {
	var input struct {
		Subtasks []string `json:"subtasks"`
	}
	if err := json.Unmarshal([]byte(params), &input); err != nil {
		return fmt.Sprintf(`{"status": "error", "message": "invalid input: %v"}`, err)
	}

	if len(input.Subtasks) < 1 || len(input.Subtasks) > 10 {
		return `{"status": "error", "message": "must provide 1-10 subtasks"}`
	}

	// Check if any subtask has already been completed (plan is locked)
	if t.onGet != nil {
		if existing, err := t.onGet(); err == nil {
			for _, st := range existing {
				if st.Status == "completed" {
					return `{"status": "error", "message": "cannot redefine subtasks after work has started (a subtask has already been completed)"}`
				}
			}
		}
	}

	if err := t.onSet(input.Subtasks); err != nil {
		return fmt.Sprintf(`{"status": "error", "message": "failed to set subtasks: %v"}`, err)
	}

	return formatSubtaskResponse("ok", input.Subtasks)
}

// getSubtasksTool returns the current subtask list with status
type getSubtasksTool struct {
	onGet func() ([]store.Subtask, error)
}

func (t *getSubtasksTool) ToolName() string { return "get_subtasks" }

func (t *getSubtasksTool) ToolDescription() string {
	return `Get all subtasks with their current status.`
}

func (t *getSubtasksTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type:       aitools.TypeObject,
		Properties: aitools.PropertyMap{},
	}
}

func (t *getSubtasksTool) Call(params string) string {
	subtasks, err := t.onGet()
	if err != nil {
		return fmt.Sprintf(`{"status": "error", "message": "%v"}`, err)
	}
	if len(subtasks) == 0 {
		return `{"status": "ok", "subtasks": [], "message": "no subtasks defined yet"}`
	}
	return formatSubtaskListResponse(subtasks)
}

// completeSubtaskTool marks the current subtask as completed
type completeSubtaskTool struct {
	onComplete func() error
	onGet      func() ([]store.Subtask, error)
}

func (t *completeSubtaskTool) ToolName() string { return "complete_subtask" }

func (t *completeSubtaskTool) ToolDescription() string {
	return `Mark the current subtask as completed and advance to the next one. Completes the first non-completed subtask in order.`
}

func (t *completeSubtaskTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type:       aitools.TypeObject,
		Properties: aitools.PropertyMap{},
	}
}

func (t *completeSubtaskTool) Call(params string) string {
	if err := t.onComplete(); err != nil {
		return fmt.Sprintf(`{"status": "error", "message": "%v"}`, err)
	}

	// Return updated list
	subtasks, err := t.onGet()
	if err != nil {
		return `{"status": "ok", "message": "subtask completed (could not fetch updated list)"}`
	}
	return formatSubtaskListResponse(subtasks)
}

// formatSubtaskResponse formats the response after setting subtasks
func formatSubtaskResponse(status string, titles []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`{"status": "%s", "count": %d, "subtasks": [`, status, len(titles)))
	for i, title := range titles {
		if i > 0 {
			sb.WriteString(", ")
		}
		st := "pending"
		if i == 0 {
			st = "in_progress"
		}
		sb.WriteString(fmt.Sprintf(`{"index": %d, "title": %q, "status": "%s"}`, i, title, st))
	}
	sb.WriteString("]}")
	return sb.String()
}

// formatSubtaskListResponse formats the subtask list for tool output
func formatSubtaskListResponse(subtasks []store.Subtask) string {
	var sb strings.Builder
	sb.WriteString(`{"status": "ok", "subtasks": [`)
	for i, st := range subtasks {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf(`{"index": %d, "title": %q, "status": "%s"}`, st.Index, st.Title, st.Status))
	}
	sb.WriteString("]}")
	return sb.String()
}
