package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"squadron/agent"
	"squadron/llm"
	"squadron/store"
)

// CommanderModelSession is the model-session surface required by the mission
// commander. llm.Session implements it directly. Configured tools are
// harness-owned orchestration controls, never task capabilities.
type CommanderModelSession interface {
	SetTools([]llm.ToolDefinition)
	SendMessageStream(context.Context, llm.Message, func(llm.StreamChunk)) (*llm.ChatResponse, error)
	AddToolResults([]llm.ToolResultBlock)
	LoadMessages([]llm.Message)
}

// ModelCommander adapts one persistent model session to CommanderDriver.
type ModelCommander struct {
	session CommanderModelSession
}

// CommanderBootstrap is the immutable, model-visible run context. Callers
// must place only non-secret values in VisibleInputs; credentials and secret
// bindings are never part of a commander prompt.
type CommanderBootstrap struct {
	MissionName          string
	Directive            string
	Tasks                []TaskDefinition
	VisibleInputs        map[string]any
	Policies             map[string]any
	ConfigurationVersion string
}

// NewModelCommanderWithProvider creates the production llm.Session used by a
// mission commander. The session receives intrinsic controls only.
func NewModelCommanderWithProvider(provider llm.Provider, model string, bootstrap CommanderBootstrap, reasoning string, restored []CommanderMessage) (*ModelCommander, *llm.Session, error) {
	if provider == nil {
		return nil, nil, fmt.Errorf("commander provider is required")
	}
	if model == "" {
		return nil, nil, fmt.Errorf("commander model is required")
	}
	prompt, err := commanderSystemPrompt(bootstrap)
	if err != nil {
		return nil, nil, err
	}
	session := llm.NewSession(provider, model, prompt)
	if reasoning != "" {
		session.SetReasoning(reasoning)
	}
	commander, err := NewModelCommander(session, restored)
	if err != nil {
		return nil, nil, err
	}
	return commander, session, nil
}

func commanderSystemPrompt(bootstrap CommanderBootstrap) (string, error) {
	if bootstrap.MissionName == "" {
		return "", fmt.Errorf("mission name is required")
	}
	contextJSON, err := json.Marshal(struct {
		MissionName          string           `json:"mission_name"`
		Directive            string           `json:"directive,omitempty"`
		Tasks                []TaskDefinition `json:"tasks"`
		VisibleInputs        map[string]any   `json:"inputs,omitempty"`
		Policies             map[string]any   `json:"policies,omitempty"`
		ConfigurationVersion string           `json:"configuration_version,omitempty"`
	}{
		MissionName: bootstrap.MissionName, Directive: bootstrap.Directive, Tasks: bootstrap.Tasks,
		VisibleInputs: bootstrap.VisibleInputs, Policies: bootstrap.Policies, ConfigurationVersion: bootstrap.ConfigurationVersion,
	})
	if err != nil {
		return "", fmt.Errorf("encode commander bootstrap: %w", err)
	}
	return `You are the single commander for this mission run.

Follow the declared mission graph. The harness is the sole authority for task eligibility and state transitions. When pending_routes is non-empty, use the bounded result summary when it is sufficient. Call inspect_task when you need the complete accepted output. If that output still lacks one specific fact needed to route, call clarify_task_result with a focused question; this restores the same task session without tools. Then call select_route with one declared target or "none". Otherwise select exactly one task from ready_tasks and call start_task. Never claim that a task started or a route was selected until the harness accepts it.

You cannot execute mission or task capabilities. Tools assigned to the mission and its tasks are capability grants for task executors and are intentionally unavailable to you. Never request credentials or secret values.

Mission context:
` + string(contextJSON), nil
}

func NewModelCommander(session CommanderModelSession, restored []CommanderMessage) (*ModelCommander, error) {
	if session == nil {
		return nil, fmt.Errorf("commander model session is required")
	}
	messages := make([]llm.Message, 0, len(restored))
	for _, message := range restored {
		converted, err := agent.MessageFromStored(store.StructuredMessage{Role: message.Role, Parts: message.Parts})
		if err != nil {
			return nil, fmt.Errorf("restore commander message %q: %w", message.ID, err)
		}
		messages = append(messages, converted)
	}
	session.LoadMessages(messages)
	session.SetTools([]llm.ToolDefinition{
		{Name: "start_task", Description: "Request that the mission harness start one task from the current ready set.", InputSchema: json.RawMessage(`{"type":"object","properties":{"task_name":{"type":"string"},"model":{"type":"string"},"tools":{"type":"array","items":{"type":"string"}}},"required":["task_name","model"],"additionalProperties":false}`)},
		{Name: "select_route", Description: "Choose one declared target for a completed router task, or none when no condition applies. For a mission target, include its non-secret string inputs.", InputSchema: json.RawMessage(`{"type":"object","properties":{"task_name":{"type":"string"},"target":{"type":"string"},"inputs":{"type":"object","additionalProperties":{"type":"string"}}},"required":["task_name","target"],"additionalProperties":false}`)},
		{Name: "inspect_task", Description: "Read the complete accepted output for a completed task. Worker turns, reasoning, tool calls, and raw provider output are excluded.", InputSchema: json.RawMessage(`{"type":"object","properties":{"task_name":{"type":"string"}},"required":["task_name"],"additionalProperties":false}`)},
		{Name: "clarify_task_result", Description: "Ask the completed task worker one focused follow-up using its persisted session. Tools are disabled and the number of clarifications is bounded.", InputSchema: json.RawMessage(`{"type":"object","properties":{"task_name":{"type":"string"},"question":{"type":"string","maxLength":4096}},"required":["task_name","question"],"additionalProperties":false}`)},
	})
	return &ModelCommander{session: session}, nil
}

func (c *ModelCommander) Decide(ctx context.Context, snapshot Snapshot, _ []CommanderMessage) (CommanderDecision, error) {
	promptJSON, err := json.Marshal(struct {
		RunID         string                       `json:"run_id"`
		Revision      uint64                       `json:"ready_set_revision"`
		Ready         []string                     `json:"ready_tasks"`
		Tasks         map[string]TaskSnapshot      `json:"tasks"`
		Results       map[string]TaskResultSummary `json:"results,omitempty"`
		PendingRoutes map[string][]RouteDefinition `json:"pending_routes,omitempty"`
	}{
		RunID: snapshot.RunID, Revision: snapshot.Revision, Ready: snapshot.Ready, Tasks: snapshot.Tasks,
		Results: snapshot.Results, PendingRoutes: snapshot.PendingRoutes,
	})
	if err != nil {
		return CommanderDecision{}, fmt.Errorf("encode commander snapshot: %w", err)
	}
	prompt := llm.NewTextMessage(llm.RoleUser, string(promptJSON))
	response, err := c.session.SendMessageStream(ctx, prompt, nil)
	if err != nil {
		return CommanderDecision{}, err
	}

	var requested *llm.ToolUseBlock
	for _, block := range response.ContentBlocks {
		if block.Type != llm.ContentTypeToolUse || block.ToolUse == nil {
			continue
		}
		if requested != nil {
			return CommanderDecision{}, fmt.Errorf("commander returned multiple control calls")
		}
		if block.ToolUse.Name != "start_task" && block.ToolUse.Name != "select_route" && block.ToolUse.Name != "inspect_task" && block.ToolUse.Name != "clarify_task_result" {
			return CommanderDecision{}, fmt.Errorf("commander requested unsupported control %q", block.ToolUse.Name)
		}
		requested = block.ToolUse
	}
	if requested == nil {
		return CommanderDecision{}, fmt.Errorf("commander must request an orchestration control")
	}
	if len(snapshot.PendingRoutes) > 0 && requested.Name != "select_route" && requested.Name != "inspect_task" && requested.Name != "clarify_task_result" {
		return CommanderDecision{}, fmt.Errorf("commander must select a pending route before starting another task")
	}
	if len(snapshot.PendingRoutes) == 0 && requested.Name != "start_task" {
		return CommanderDecision{}, fmt.Errorf("commander requested select_route without a pending route")
	}

	messageID := response.ID
	if messageID == "" {
		messageID = requested.ID + ":assistant"
	}
	assistantMessage := llm.Message{Role: llm.RoleAssistant, Content: response.Content, Parts: response.ContentBlocks}
	decision := CommanderDecision{
		Prompt:  CommanderMessage{ID: messageID + ":prompt", Role: string(llm.RoleUser), Parts: agent.PartsFromMessage(prompt)},
		Message: CommanderMessage{ID: messageID, Role: string(llm.RoleAssistant), Parts: agent.PartsFromMessage(assistantMessage)},
	}
	if requested.Name == "clarify_task_result" {
		var input struct {
			TaskName string `json:"task_name"`
			Question string `json:"question"`
		}
		if err := json.Unmarshal(requested.Input, &input); err != nil {
			return CommanderDecision{}, fmt.Errorf("decode clarify_task_result input: %w", err)
		}
		if input.TaskName == "" || input.Question == "" {
			return CommanderDecision{}, fmt.Errorf("clarify_task_result task_name and question are required")
		}
		decision.Clarify = ClarifyTaskRequest{RequestID: requested.ID, MessageID: messageID, TaskName: input.TaskName, Question: input.Question, Revision: snapshot.Revision}
		return decision, nil
	}
	if requested.Name == "inspect_task" {
		var input struct {
			TaskName string `json:"task_name"`
		}
		if err := json.Unmarshal(requested.Input, &input); err != nil {
			return CommanderDecision{}, fmt.Errorf("decode inspect_task input: %w", err)
		}
		if input.TaskName == "" {
			return CommanderDecision{}, fmt.Errorf("inspect_task task_name is required")
		}
		decision.Inspect = InspectTaskRequest{RequestID: requested.ID, TaskName: input.TaskName, Revision: snapshot.Revision}
		return decision, nil
	}
	if requested.Name == "select_route" {
		var input struct {
			TaskName string            `json:"task_name"`
			Target   string            `json:"target"`
			Inputs   map[string]string `json:"inputs"`
		}
		if err := json.Unmarshal(requested.Input, &input); err != nil {
			return CommanderDecision{}, fmt.Errorf("decode select_route input: %w", err)
		}
		if input.TaskName == "" || input.Target == "" {
			return CommanderDecision{}, fmt.Errorf("select_route task_name and target are required")
		}
		decision.Route = RouteRequest{RequestID: requested.ID, TaskName: input.TaskName, Target: input.Target, Inputs: input.Inputs, Revision: snapshot.Revision}
		return decision, nil
	}
	var input struct {
		TaskName string   `json:"task_name"`
		Model    string   `json:"model"`
		Tools    []string `json:"tools"`
	}
	if err := json.Unmarshal(requested.Input, &input); err != nil {
		return CommanderDecision{}, fmt.Errorf("decode start_task input: %w", err)
	}
	if input.TaskName == "" {
		return CommanderDecision{}, fmt.Errorf("start_task task_name is required")
	}
	if input.Model == "" {
		return CommanderDecision{}, fmt.Errorf("start_task model is required")
	}

	decision.Dispatch = DispatchRequest{RequestID: requested.ID, TaskName: input.TaskName, ReadySetRevision: snapshot.Revision}
	decision.Grant = GrantRequest{TaskName: input.TaskName, Model: input.Model, Tools: append([]string(nil), input.Tools...)}
	return decision, nil
}

func (c *ModelCommander) DispatchAccepted(dispatch Dispatch) {
	c.session.AddToolResults([]llm.ToolResultBlock{{
		ToolUseID: dispatch.RequestID,
		Content:   dispatchResultText(dispatch),
	}})
}

func (c *ModelCommander) RouteAccepted(selection RouteSelection) {
	payload, _ := json.Marshal(map[string]any{"accepted": true, "task": selection.TaskName, "target": selection.Target})
	c.session.AddToolResults([]llm.ToolResultBlock{{ToolUseID: selection.RequestID, Content: string(payload)}})
}

func (c *ModelCommander) InspectionAccepted(inspection TaskInspection) {
	payload, _ := json.Marshal(inspection)
	c.session.AddToolResults([]llm.ToolResultBlock{{ToolUseID: inspection.RequestID, Content: string(payload)}})
}

func (c *ModelCommander) ClarificationAccepted(clarification TaskClarification) {
	payload, _ := json.Marshal(clarification)
	c.session.AddToolResults([]llm.ToolResultBlock{{ToolUseID: clarification.RequestID, Content: string(payload)}})
}
