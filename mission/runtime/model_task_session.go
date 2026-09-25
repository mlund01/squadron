package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"squadron/agent"
	"squadron/aitools"
	"squadron/llm"
	"squadron/store"
)

const maxTaskSessionTurns = 100

// TaskModelSession is the provider-backed session surface used by an
// ephemeral task executor. llm.Session implements it directly.
type TaskModelSession interface {
	SetTools([]llm.ToolDefinition)
	SendStream(context.Context, string, func(llm.StreamChunk)) (*llm.ChatResponse, error)
	ContinueStream(context.Context, func(llm.StreamChunk)) (*llm.ChatResponse, error)
	AddToolResults([]llm.ToolResultBlock)
	LoadMessages([]llm.Message)
	SnapshotMessages() []llm.Message
}

// TaskModelSessionBuilder resolves the persisted grant's model and constructs
// a fresh provider session for one attempt.
type TaskModelSessionBuilder interface {
	BuildTaskModelSession(context.Context, Dispatch) (TaskModelSession, error)
}

// TaskObjective supplies immutable task instructions without giving the
// executor access to mission orchestration operations.
type TaskObjective struct {
	Name         string
	Objective    string
	Routes       []RouteDefinition
	ResultPolicy ResultPolicy
}

// ModelTaskSessionFactory creates ephemeral model sessions for BoundedExecutor.
type ModelTaskSessionFactory struct {
	builder    TaskModelSessionBuilder
	objectives map[string]TaskObjective
}

func NewModelTaskSessionFactory(builder TaskModelSessionBuilder, objectives []TaskObjective) (*ModelTaskSessionFactory, error) {
	if builder == nil {
		return nil, fmt.Errorf("task model session builder is required")
	}
	byName := make(map[string]TaskObjective, len(objectives))
	for _, objective := range objectives {
		if objective.Name == "" || objective.Objective == "" {
			return nil, fmt.Errorf("task name and objective are required")
		}
		if _, exists := byName[objective.Name]; exists {
			return nil, fmt.Errorf("duplicate task objective %q", objective.Name)
		}
		objective.ResultPolicy = cloneResultPolicy(objective.ResultPolicy)
		objective.Routes = append([]RouteDefinition(nil), objective.Routes...)
		byName[objective.Name] = objective
	}
	return &ModelTaskSessionFactory{builder: builder, objectives: byName}, nil
}

func (f *ModelTaskSessionFactory) NewTaskSession(ctx context.Context, dispatch Dispatch, tools map[string]aitools.Tool) (TaskSession, error) {
	objective, ok := f.objectives[dispatch.TaskName]
	if !ok && dispatch.Grant.Iteration != nil {
		objective, ok = f.objectives[dispatch.Grant.Iteration.ParentTask]
	}
	if !ok {
		return nil, fmt.Errorf("task objective %q not found", dispatch.TaskName)
	}
	session, err := f.builder.BuildTaskModelSession(ctx, cloneDispatch(dispatch))
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("task model session builder returned nil")
	}
	toolsByModelName := make(map[string]aitools.Tool, len(tools))
	for name, tool := range tools {
		modelName := aitools.SanitizeToolName(name)
		if _, exists := toolsByModelName[modelName]; exists {
			return nil, fmt.Errorf("granted tools collide on model name %q", modelName)
		}
		toolsByModelName[modelName] = tool
	}
	session.SetTools(aitools.ToolsToDefinitions(tools))
	return &modelTaskSession{
		session: session, dispatch: cloneDispatch(dispatch), objective: objective.Objective,
		resultPolicy: cloneResultPolicy(objective.ResultPolicy), tools: toolsByModelName,
		routes: append([]RouteDefinition(nil), objective.Routes...),
	}, nil
}

// Clarify restores the exact persisted worker transcript into a fresh
// provider session, removes every tool, and executes one focused follow-up.
func (f *ModelTaskSessionFactory) Clarify(ctx context.Context, dispatch Dispatch, transcript []TaskSessionMessage, question string) (TaskClarificationResult, error) {
	if strings.TrimSpace(question) == "" {
		return TaskClarificationResult{}, fmt.Errorf("clarification question is required")
	}
	_, ok := f.objectives[dispatch.TaskName]
	if !ok && dispatch.Grant.Iteration != nil {
		_, ok = f.objectives[dispatch.Grant.Iteration.ParentTask]
	}
	if !ok {
		return TaskClarificationResult{}, fmt.Errorf("task objective %q not found", dispatch.TaskName)
	}
	messages, err := taskSessionMessagesToLLM(transcript)
	if err != nil {
		return TaskClarificationResult{}, err
	}
	session, err := f.builder.BuildTaskModelSession(ctx, cloneDispatch(dispatch))
	if err != nil {
		return TaskClarificationResult{}, err
	}
	if session == nil {
		return TaskClarificationResult{}, fmt.Errorf("task model session builder returned nil")
	}
	defer func() {
		if closer, ok := session.(interface{ Close() }); ok {
			closer.Close()
		}
	}()
	session.LoadMessages(messages)
	session.SetTools(nil)
	prompt, err := json.Marshal(map[string]string{
		"instruction": "Answer this focused follow-up only from the work and evidence already present in this task session. Do not perform new work and do not call tools.",
		"question":    question,
	})
	if err != nil {
		return TaskClarificationResult{}, fmt.Errorf("encode clarification prompt: %w", err)
	}
	response, err := session.SendStream(ctx, string(prompt), nil)
	if err != nil {
		return TaskClarificationResult{}, err
	}
	if len(taskToolUses(response)) > 0 {
		return TaskClarificationResult{}, fmt.Errorf("task worker attempted a tool call during clarification")
	}
	if response == nil {
		return TaskClarificationResult{}, fmt.Errorf("task worker returned no clarification")
	}
	blocks := append([]llm.ContentBlock(nil), response.ContentBlocks...)
	if len(blocks) == 0 && response.Content != "" {
		blocks = []llm.ContentBlock{{Type: llm.ContentTypeText, Text: response.Content}}
	}
	answer := agent.PartsFromMessage(llm.Message{Role: llm.RoleAssistant, Parts: blocks, Content: response.Content})
	if len(answer) == 0 {
		return TaskClarificationResult{}, fmt.Errorf("task worker returned an empty clarification")
	}
	updatedTranscript, err := captureTaskTurnTelemetry(
		taskSessionMessagesFromLLM(session.SnapshotMessages()), transcript, response,
		dispatch.AttemptID, "task", dispatch.TaskName, dispatch.Grant.Model,
	)
	if err != nil {
		return TaskClarificationResult{}, err
	}
	return TaskClarificationResult{Answer: answer, SessionTranscript: updatedTranscript}, nil
}

type modelTaskSession struct {
	session      TaskModelSession
	dispatch     Dispatch
	objective    string
	resultPolicy ResultPolicy
	routes       []RouteDefinition
	tools        map[string]aitools.Tool
}

func (s *modelTaskSession) Run(ctx context.Context) (TaskResultEnvelope, error) {
	return s.RunResumable(ctx, TaskAttemptProgress{AttemptID: s.dispatch.AttemptID, Phase: "dispatched", Tools: make(map[string]ToolOperation)}, func(TaskAttemptProgress) error { return nil })
}

func (s *modelTaskSession) RunResumable(ctx context.Context, progress TaskAttemptProgress, commit func(TaskAttemptProgress) error) (TaskResultEnvelope, error) {
	if progress.AttemptID == "" {
		progress.AttemptID = s.dispatch.AttemptID
	}
	if progress.AttemptID != s.dispatch.AttemptID {
		return TaskResultEnvelope{}, fmt.Errorf("task progress belongs to attempt %q, want %q", progress.AttemptID, s.dispatch.AttemptID)
	}
	if progress.Tools == nil {
		progress.Tools = make(map[string]ToolOperation)
	}
	if len(progress.Transcript) > 0 {
		messages, err := taskSessionMessagesToLLM(progress.Transcript)
		if err != nil {
			return TaskResultEnvelope{}, err
		}
		s.session.LoadMessages(messages)
	}

	inputJSON := json.RawMessage(s.dispatch.Grant.InputsJSON)
	if len(inputJSON) == 0 {
		inputJSON = json.RawMessage(`{}`)
	}
	if !json.Valid(inputJSON) {
		return TaskResultEnvelope{}, fmt.Errorf("task input snapshot is invalid JSON")
	}
	prompt, err := json.Marshal(struct {
		Task            string               `json:"task"`
		Objective       string               `json:"objective"`
		Inputs          json.RawMessage      `json:"inputs"`
		Iteration       *IterationInput      `json:"iteration,omitempty"`
		Dependencies    []DependencySnapshot `json:"dependencies,omitempty"`
		OutputContract  []ResultField        `json:"outputContract,omitempty"`
		OutputRule      string               `json:"outputRule,omitempty"`
		RoutingCriteria []RouteDefinition    `json:"routingCriteria,omitempty"`
		RoutingRule     string               `json:"routingRule,omitempty"`
	}{
		Task: s.dispatch.TaskName, Objective: s.objective, Inputs: inputJSON, Iteration: s.dispatch.Grant.Iteration,
		Dependencies:    s.dispatch.Grant.Dependencies,
		OutputContract:  s.resultPolicy.Fields,
		OutputRule:      outputRule(s.resultPolicy),
		RoutingCriteria: s.routes,
		RoutingRule:     routingRule(s.routes),
	})
	if err != nil {
		return TaskResultEnvelope{}, fmt.Errorf("encode task prompt: %w", err)
	}

	var response *llm.ChatResponse
	if len(progress.Transcript) == 0 || progress.Phase == "dispatched" {
		response, err = s.session.SendStream(ctx, string(prompt), nil)
		if err != nil {
			return TaskResultEnvelope{}, err
		}
		progress.Transcript, err = captureTaskTurnTelemetry(
			taskSessionMessagesFromLLM(s.session.SnapshotMessages()), progress.Transcript, response,
			s.dispatch.AttemptID, "task", s.dispatch.TaskName, s.dispatch.Grant.Model,
		)
		if err != nil {
			return TaskResultEnvelope{}, err
		}
	} else if progress.Phase == "tool_results_added" {
		response, err = s.session.ContinueStream(ctx, nil)
		if err != nil {
			return TaskResultEnvelope{}, err
		}
		progress.Transcript, err = captureTaskTurnTelemetry(
			taskSessionMessagesFromLLM(s.session.SnapshotMessages()), progress.Transcript, response,
			s.dispatch.AttemptID, "task", s.dispatch.TaskName, s.dispatch.Grant.Model,
		)
		if err != nil {
			return TaskResultEnvelope{}, err
		}
	} else {
		response, err = responseFromTaskTranscript(progress.Transcript)
		if err != nil {
			return TaskResultEnvelope{}, err
		}
	}
	for turn := 0; turn < maxTaskSessionTurns; turn++ {
		toolUses := taskToolUses(response)
		if len(toolUses) == 0 {
			progress.Transcript = mergeTaskTurnTelemetry(taskSessionMessagesFromLLM(s.session.SnapshotMessages()), progress.Transcript)
			progress.Phase = "final_response"
			if err := commit(progress); err != nil {
				return TaskResultEnvelope{}, &TaskRecoveryBlockedError{Err: fmt.Errorf("checkpoint final task response: %w", err)}
			}
			result, err := taskResultFromResponse(s.dispatch, response)
			if err != nil {
				return TaskResultEnvelope{}, err
			}
			result.SessionTranscript = cloneTaskSessionMessages(progress.Transcript)
			return result, nil
		}
		progress.Transcript = mergeTaskTurnTelemetry(taskSessionMessagesFromLLM(s.session.SnapshotMessages()), progress.Transcript)
		progress.Phase = "tool_calls_received"
		for _, call := range toolUses {
			if _, exists := progress.Tools[call.ID]; !exists {
				progress.Tools[call.ID] = ToolOperation{ToolUseID: call.ID, ToolName: call.Name, InputJSON: string(call.Input), Status: ToolOperationIntended}
			}
		}
		if err := commit(progress); err != nil {
			return TaskResultEnvelope{}, &TaskRecoveryBlockedError{Err: fmt.Errorf("checkpoint task tool intent: %w", err)}
		}
		results := make([]llm.ToolResultBlock, 0, len(toolUses))
		for _, call := range toolUses {
			operation := progress.Tools[call.ID]
			tool := s.tools[call.Name]
			switch operation.Status {
			case ToolOperationCompleted:
				results = append(results, llm.ToolResultBlock{ToolUseID: call.ID, Content: operation.Result, IsError: operation.IsError})
				continue
			case ToolOperationInFlight:
				if !aitools.IsReplaySafe(tool) {
					return TaskResultEnvelope{}, &TaskRecoveryBlockedError{
						Err:       fmt.Errorf("tool call %q has an uncertain external outcome", call.ID),
						ToolUseID: call.ID, ToolName: call.Name,
						IdempotencyKey: aitools.StableIdempotencyKey(s.dispatch.RunID, s.dispatch.AttemptID, call.ID),
					}
				}
			case ToolOperationIntended:
			default:
				return TaskResultEnvelope{}, fmt.Errorf("tool call %q has invalid durable status %q", call.ID, operation.Status)
			}
			if operation.Status == ToolOperationIntended {
				operation.Status = ToolOperationInFlight
				if operation.StartedAt.IsZero() {
					operation.StartedAt = time.Now().UTC()
				}
				progress.Tools[call.ID] = operation
				if err := commit(progress); err != nil {
					return TaskResultEnvelope{}, &TaskRecoveryBlockedError{Err: fmt.Errorf("checkpoint tool call %q before invocation: %w", call.ID, err)}
				}
			}
			var toolResult llm.ToolResultBlock
			if tool == nil {
				toolResult = llm.ToolResultBlock{ToolUseID: call.ID, Content: fmt.Sprintf("Error: tool %q is not granted", call.Name), IsError: true}
			} else {
				metadata := aitools.InvocationMetadata{
					RunID: s.dispatch.RunID, TaskName: s.dispatch.TaskName, AttemptID: s.dispatch.AttemptID,
					ToolUseID: call.ID, IdempotencyKey: aitools.StableIdempotencyKey(s.dispatch.RunID, s.dispatch.AttemptID, call.ID),
					StartedAt: operation.StartedAt,
				}
				toolCtx := aitools.WithInvocationMetadata(ctx, metadata)
				toolResult = llm.ToolResultBlock{ToolUseID: call.ID, Content: tool.Call(toolCtx, string(call.Input))}
			}
			// Cancellation is not a tool result. Preserve the in-flight record so
			// recovery can reconnect/replay a declared-safe operation or visibly
			// reconcile an uncertain one instead of feeding a synthetic answer to
			// the model.
			if err := ctx.Err(); err != nil {
				return TaskResultEnvelope{}, err
			}
			operation.Status = ToolOperationCompleted
			operation.Result = toolResult.Content
			operation.IsError = toolResult.IsError
			progress.Tools[call.ID] = operation
			if err := commit(progress); err != nil {
				return TaskResultEnvelope{}, &TaskRecoveryBlockedError{Err: fmt.Errorf("checkpoint tool call %q result: %w", call.ID, err)}
			}
			results = append(results, toolResult)
		}
		s.session.AddToolResults(results)
		progress.Transcript = mergeTaskTurnTelemetry(taskSessionMessagesFromLLM(s.session.SnapshotMessages()), progress.Transcript)
		progress.Phase = "tool_results_added"
		if err := commit(progress); err != nil {
			return TaskResultEnvelope{}, &TaskRecoveryBlockedError{Err: fmt.Errorf("checkpoint task tool results: %w", err)}
		}
		response, err = s.session.ContinueStream(ctx, nil)
		if err != nil {
			return TaskResultEnvelope{}, err
		}
		progress.Transcript, err = captureTaskTurnTelemetry(
			taskSessionMessagesFromLLM(s.session.SnapshotMessages()), progress.Transcript, response,
			s.dispatch.AttemptID, "task", s.dispatch.TaskName, s.dispatch.Grant.Model,
		)
		if err != nil {
			return TaskResultEnvelope{}, err
		}
	}
	return TaskResultEnvelope{}, fmt.Errorf("task session exceeded %d turns", maxTaskSessionTurns)
}

func captureTaskTurnTelemetry(current, previous []TaskSessionMessage, response *llm.ChatResponse, sessionID, entity, taskName, model string) ([]TaskSessionMessage, error) {
	current = mergeTaskTurnTelemetry(current, previous)
	if response == nil {
		return nil, fmt.Errorf("cannot capture telemetry for an empty model response")
	}
	assistantIndex := -1
	for index := len(current) - 1; index >= 0; index-- {
		if current[index].Role == string(llm.RoleAssistant) {
			assistantIndex = index
			break
		}
	}
	if assistantIndex < 0 {
		return nil, fmt.Errorf("model response did not produce an assistant transcript message")
	}
	if current[assistantIndex].Telemetry != nil {
		return nil, fmt.Errorf("assistant transcript message already has turn telemetry")
	}
	ordinal := 1
	for _, message := range current {
		if message.Telemetry != nil {
			ordinal++
		}
	}
	responseID := response.ID
	if responseID == "" {
		responseID = "response"
	}
	current[assistantIndex].Telemetry = &ModelTurnTelemetry{
		ID:        sessionID + ":turn:" + fmt.Sprint(ordinal) + ":" + responseID,
		SessionID: sessionID, Entity: entity, TaskName: taskName, Model: model, FinishReason: response.FinishReason,
		InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens,
		CacheWriteTokens: response.Usage.CacheWriteTokens, CacheReadTokens: response.Usage.CacheReadTokens,
	}
	return current, nil
}

func mergeTaskTurnTelemetry(current, previous []TaskSessionMessage) []TaskSessionMessage {
	for index := 0; index < len(current) && index < len(previous); index++ {
		if previous[index].Telemetry == nil {
			continue
		}
		telemetry := *previous[index].Telemetry
		current[index].Telemetry = &telemetry
	}
	return current
}

func responseFromTaskTranscript(transcript []TaskSessionMessage) (*llm.ChatResponse, error) {
	if len(transcript) == 0 {
		return nil, fmt.Errorf("task transcript is empty")
	}
	messages, err := taskSessionMessagesToLLM(transcript[len(transcript)-1:])
	if err != nil {
		return nil, err
	}
	message := messages[0]
	if message.Role != llm.RoleAssistant {
		return nil, fmt.Errorf("task transcript does not end with an assistant response")
	}
	return &llm.ChatResponse{Content: message.GetTextContent(), ContentBlocks: message.Parts}, nil
}

func taskSessionMessagesFromLLM(messages []llm.Message) []TaskSessionMessage {
	result := make([]TaskSessionMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, TaskSessionMessage{Role: string(message.Role), Parts: agent.PartsFromMessage(message)})
	}
	return result
}

func taskSessionMessagesToLLM(messages []TaskSessionMessage) ([]llm.Message, error) {
	result := make([]llm.Message, 0, len(messages))
	for index, message := range messages {
		converted, err := agent.MessageFromStored(store.StructuredMessage{Role: message.Role, Parts: message.Parts})
		if err != nil {
			return nil, fmt.Errorf("restore task session message %d: %w", index, err)
		}
		result = append(result, converted)
	}
	return result, nil
}

func routingRule(routes []RouteDefinition) string {
	if len(routes) == 0 {
		return ""
	}
	return "Do not choose a route. Surface concrete evidence relevant to these conditions in your final summary and structured output so the mission commander can decide."
}

func outputRule(policy ResultPolicy) string {
	if len(policy.Fields) == 0 {
		return "Return a natural-language or rich result."
	}
	return "Return only one JSON object matching outputContract. The harness rejects data that does not match."
}

func taskResultFromResponse(dispatch Dispatch, response *llm.ChatResponse) (TaskResultEnvelope, error) {
	if response == nil {
		return TaskResultEnvelope{}, fmt.Errorf("task model returned no response")
	}
	blocks := append([]llm.ContentBlock(nil), response.ContentBlocks...)
	if len(blocks) == 0 && response.Content != "" {
		blocks = []llm.ContentBlock{{Type: llm.ContentTypeText, Text: response.Content}}
	}
	raw := agent.PartsFromMessage(llm.Message{Role: llm.RoleAssistant, Parts: blocks, Content: response.Content})
	contentBlocks := make([]llm.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case llm.ContentTypeText, llm.ContentTypeImage, llm.ContentTypeDocument:
			contentBlocks = append(contentBlocks, block)
		}
	}
	content := agent.PartsFromMessage(llm.Message{Role: llm.RoleAssistant, Parts: contentBlocks})
	text := strings.TrimSpace(response.Content)
	if text == "" {
		text = strings.TrimSpace(llm.Message{Parts: contentBlocks}.GetTextContent())
	}
	summary := text
	if summary == "" && len(content) > 0 {
		summary = "Rich task result"
	}
	if utf8.RuneCountInString(summary) > 512 {
		runes := []rune(summary)
		summary = strings.TrimSpace(string(runes[:509])) + "..."
	}
	var data json.RawMessage
	if text != "" && json.Valid([]byte(text)) {
		data = append(json.RawMessage(nil), []byte(text)...)
	}
	diagnostics, err := json.Marshal(struct {
		FinishReason string    `json:"finishReason,omitempty"`
		Usage        llm.Usage `json:"usage"`
	}{FinishReason: response.FinishReason, Usage: response.Usage})
	if err != nil {
		return TaskResultEnvelope{}, fmt.Errorf("encode result diagnostics: %w", err)
	}
	return TaskResultEnvelope{
		Status: ResultSucceeded, Summary: summary, Content: content, Data: data,
		Diagnostics: diagnostics, RawOutput: raw,
		Provenance: ResultProvenance{
			RunID: dispatch.RunID, TaskName: dispatch.TaskName, AttemptID: dispatch.AttemptID,
			Model: dispatch.Grant.Model, RunAs: dispatch.Grant.RunAs,
			ConfigurationVersion: dispatch.Grant.ConfigurationVersion,
		},
	}, nil
}

func (s *modelTaskSession) Close() error {
	if closer, ok := s.session.(interface{ Close() }); ok {
		closer.Close()
	}
	return nil
}

func taskToolUses(response *llm.ChatResponse) []*llm.ToolUseBlock {
	if response == nil {
		return nil
	}
	var result []*llm.ToolUseBlock
	for _, block := range response.ContentBlocks {
		if block.Type == llm.ContentTypeToolUse && block.ToolUse != nil {
			result = append(result, block.ToolUse)
		}
	}
	return result
}
