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
	IsMission bool         `json:"isMission,omitempty"` // true if target is another mission
	Inputs    []RouteInput `json:"inputs,omitempty"`    // required inputs for mission targets
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
	// SubtaskChecker returns (total, incomplete) subtask counts.
	// If set, task_complete will fail when incomplete > 0.
	SubtaskChecker func() (total int, incomplete int)

	// Router support: when set, task_complete enters a two-phase flow
	Routes        []RouteOption      // Set by runner if task has a router (nil otherwise)
	routingPhase  bool               // true after first call returned route options
	chosenRoute   string             // the route key chosen by commander
	isMissionRoute bool              // true if chosen route is a mission
	missionInputs map[string]string  // inputs provided for mission route
}

func (t *TaskCompleteTool) ToolName() string {
	return "task_complete"
}

func (t *TaskCompleteTool) ToolDescription() string {
	if len(t.Routes) > 0 {
		hasMission := false
		for _, r := range t.Routes {
			if r.IsMission {
				hasMission = true
				break
			}
		}
		if hasMission {
			return "Signal that you have completed the task. When called, this tool may return a list of routing options for you to choose from based on your task results. Some options may launch a new mission — if the chosen route is a mission and it requires inputs, you must provide them via the 'mission_inputs' parameter. Call this tool again with the 'route' parameter set to your chosen route key, or 'none' if no route applies. You may gather more information from agents before making your routing decision. Pass succeed=false with a reason if the task cannot be completed successfully."
		}
		return "Signal that you have completed the task. When called, this tool may return a list of routing options for you to choose from based on your task results. If routing options are returned, call this tool again with the 'route' parameter set to the key of your chosen route, or 'none' if no route applies. You may gather more information from agents before making your routing decision. Pass succeed=false with a reason if the task cannot be completed successfully."
	}
	return "Signal that you have completed the task. Call this when all work is done and all subtasks are complete. Pass succeed=false with a reason if the task cannot be completed successfully."
}

func (t *TaskCompleteTool) ToolPayloadSchema() Schema {
	props := map[string]Property{
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
			Description: "The key of the chosen route. Only used when responding to routing options. Use 'none' to complete without activating any route.",
		}
		// Check if any route targets a mission with inputs
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
				Description: "Input values for a mission route target. Required when the chosen route is a mission that has required inputs. Keys are input names, values are strings.",
			}
		}
	}
	return Schema{
		Type:       TypeObject,
		Properties: props,
	}
}

func (t *TaskCompleteTool) Call(ctx context.Context, params string) string {
	// Parse params
	succeed := true
	reason := ""
	route := ""
	var missionInputs map[string]string
	if params != "" && params != "{}" {
		var input struct {
			Succeed       *bool             `json:"succeed"`
			Reason        string            `json:"reason"`
			Route         string            `json:"route"`
			MissionInputs map[string]string `json:"mission_inputs"`
		}
		if err := json.Unmarshal([]byte(params), &input); err == nil {
			if input.Succeed != nil {
				succeed = *input.Succeed
			}
			reason = input.Reason
			route = input.Route
			missionInputs = input.MissionInputs
		}
	}

	// If we're in the routing phase, handle route selection
	if t.routingPhase {
		if route == "" {
			return `{"status": "error", "error": "You must choose a route. Call task_complete with route set to one of the option keys, or 'none' to complete without routing."}`
		}
		if route == "none" {
			t.completed = true
			t.succeeded = true
			t.chosenRoute = ""
			return `{"status": "ok", "message": "Task completed without routing."}`
		}
		// Validate the chosen route
		for _, r := range t.Routes {
			if r.Target == route {
				// If it's a mission route, validate required inputs
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
		return fmt.Sprintf(`{"status": "error", "error": "Invalid route key '%s'. Choose one of the available option keys or 'none'."}`, route)
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

	// If routes are configured, enter routing phase instead of completing
	if len(t.Routes) > 0 {
		t.routingPhase = true
		options := make([]map[string]interface{}, 0, len(t.Routes)+1)
		for _, r := range t.Routes {
			opt := map[string]interface{}{
				"key":       r.Target,
				"condition": r.Condition,
			}
			if r.IsMission {
				opt["type"] = "mission"
				if len(r.Inputs) > 0 {
					opt["required_inputs"] = r.Inputs
				}
			}
			options = append(options, opt)
		}
		options = append(options, map[string]interface{}{
			"key":       "none",
			"condition": "No route applies — complete without branching",
		})
		resp, _ := json.Marshal(map[string]interface{}{
			"status":  "routing",
			"message": "Task work is complete. Based on your task results, choose the most appropriate route. Only choose a route if the condition is clearly met — if none of the conditions clearly apply, choose 'none'. If you need more information before deciding, you may call_agent first. For mission routes with required_inputs, provide values via the mission_inputs parameter. Then call task_complete with the route parameter set to your choice.",
			"options": options,
		})
		return string(resp)
	}

	// No routes — complete immediately
	t.completed = true
	t.succeeded = true
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

// ChosenRoute returns the route chosen by the commander, or "" if none.
func (t *TaskCompleteTool) ChosenRoute() string {
	return t.chosenRoute
}

// IsMissionRoute returns true if the chosen route targets a mission.
func (t *TaskCompleteTool) IsMissionRoute() bool {
	return t.isMissionRoute
}

// MissionInputs returns the inputs provided for a mission route, or nil.
func (t *TaskCompleteTool) MissionInputs() map[string]string {
	return t.missionInputs
}
