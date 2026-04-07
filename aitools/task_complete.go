package aitools

import (
	"context"
	"encoding/json"
	"fmt"
)

// RouteOption represents a routing option presented to the commander
type RouteOption struct {
	Target    string       `json:"target"`
	Condition string       `json:"condition"`
	IsMission bool         `json:"isMission,omitempty"`
	Inputs    []RouteInput `json:"inputs,omitempty"`
}

// RouteInput describes a required input for a mission route target
type RouteInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// TaskCompleteTool allows the commander to signal that it has finished the task.
type TaskCompleteTool struct {
	completed     bool
	succeeded     bool
	failureReason string
	summary       string
	// SubtaskChecker returns (total, incomplete) subtask counts.
	SubtaskChecker func() (total int, incomplete int)

	// Routing support
	Routes        []RouteOption // Set by runner if task has routes (nil otherwise)
	chosenRoute   string
	isMissionRoute bool
	missionInputs map[string]string
}

func (t *TaskCompleteTool) ToolName() string {
	return "task_complete"
}

func (t *TaskCompleteTool) ToolDescription() string {
	if len(t.Routes) > 0 {
		return "Signal that you have completed the task. Always include a summary. You MUST also include a 'route' — check the routing options in your system prompt and choose the appropriate route key, or 'none' if no route applies."
	}
	return "Signal that you have completed the task. Call this when all work is done and all subtasks are complete. Always include a summary of key findings and results. Pass succeed=false with a reason if the task cannot be completed successfully."
}

func (t *TaskCompleteTool) ToolPayloadSchema() Schema {
	props := map[string]Property{
		"summary": {
			Type:        TypeString,
			Description: "A concise summary of what was accomplished, key findings, and important results. This summary will be provided as context to downstream dependent tasks.",
		},
		"succeed": {
			Type:        TypeBoolean,
			Description: "Whether the task completed successfully. Defaults to true. Set to false to mark the task as failed.",
		},
		"reason": {
			Type:        TypeString,
			Description: "Explanation of why the task failed. Required when succeed=false.",
		},
	}
	if len(t.Routes) > 0 {
		props["route"] = Property{
			Type:        TypeString,
			Description: "The route to activate. Must be one of the route keys from the routing options in your system prompt, or 'none' if no route applies. Required when routing options are configured.",
		}
		hasMissionInputs := false
		for _, r := range t.Routes {
			if r.IsMission && len(r.Inputs) > 0 {
				hasMissionInputs = true
				break
			}
		}
		if hasMissionInputs {
			props["mission_inputs"] = Property{
				Type:        TypeObject,
				Description: "Input values for a mission route target. Required when the chosen route is a mission that has required inputs.",
			}
		}
	}
	return Schema{
		Type:       TypeObject,
		Properties: props,
	}
}

func (t *TaskCompleteTool) Call(ctx context.Context, params string) string {
	succeed := true
	reason := ""
	route := ""
	var missionInputs map[string]string

	if params != "" && params != "{}" {
		var input struct {
			Succeed       *bool             `json:"succeed"`
			Summary       string            `json:"summary"`
			Reason        string            `json:"reason"`
			Route         string            `json:"route"`
			MissionInputs map[string]string `json:"mission_inputs"`
		}
		if err := json.Unmarshal([]byte(params), &input); err == nil {
			if input.Succeed != nil {
				succeed = *input.Succeed
			}
			t.summary = input.Summary
			reason = input.Reason
			route = input.Route
			missionInputs = input.MissionInputs
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

	// Handle failure
	if !succeed {
		t.completed = true
		t.succeeded = false
		t.failureReason = reason
		return `{"status": "ok", "message": "Task marked as failed."}`
	}

	// If routes are configured, require a route selection
	if len(t.Routes) > 0 {
		if route == "" {
			return `{"status": "error", "error": "This task has routing options. You must include a 'route' parameter — choose one of the route keys from the routing options in your system prompt, or 'none' if no route applies."}`
		}
		if route == "none" {
			t.completed = true
			t.succeeded = true
			t.chosenRoute = ""
			return `{"status": "ok", "message": "Task completed without routing."}`
		}
		for _, r := range t.Routes {
			if r.Target == route {
				if r.IsMission {
					for _, inp := range r.Inputs {
						if inp.Required {
							if missionInputs == nil || missionInputs[inp.Name] == "" {
								return fmt.Sprintf(`{"status": "error", "error": "Mission '%s' requires input '%s' (%s). Provide it via mission_inputs."}`, route, inp.Name, inp.Description)
							}
						}
					}
				}
				t.completed = true
				t.succeeded = true
				t.chosenRoute = route
				t.isMissionRoute = r.IsMission
				t.missionInputs = missionInputs
				if r.IsMission {
					return fmt.Sprintf(`{"status": "ok", "routed_to_mission": "%s"}`, route)
				}
				return fmt.Sprintf(`{"status": "ok", "routed_to": "%s"}`, route)
			}
		}
		return fmt.Sprintf(`{"status": "error", "error": "Invalid route key '%s'. Choose one of the route keys from the routing options in your system prompt, or 'none'."}`, route)
	}

	// No routes — complete immediately
	t.completed = true
	t.succeeded = true
	return `{"status": "ok"}`
}

func (t *TaskCompleteTool) IsCompleted() bool             { return t.completed }
func (t *TaskCompleteTool) IsSucceeded() bool             { return t.succeeded }
func (t *TaskCompleteTool) FailureReason() string         { return t.failureReason }
func (t *TaskCompleteTool) Summary() string               { return t.summary }
func (t *TaskCompleteTool) ChosenRoute() string           { return t.chosenRoute }
func (t *TaskCompleteTool) IsMissionRoute() bool          { return t.isMissionRoute }
func (t *TaskCompleteTool) MissionInputs() map[string]string { return t.missionInputs }
