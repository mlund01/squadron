package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"squadron/agent"
	"squadron/aitools"
	"squadron/llm"
	"squadron/store"
)

func TestModelTaskSessionRunsGrantedToolsInFreshProviderSession(t *testing.T) {
	t.Parallel()

	modelSession := &fakeTaskModelSession{responses: []*llm.ChatResponse{
		{ContentBlocks: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{
			ID: "call-1", Name: "plugins_search", Input: json.RawMessage(`{"query":"durability"}`),
		}}}},
		{Content: "done"},
	}}
	builder := &fakeTaskModelSessionBuilder{session: modelSession}
	factory, err := NewModelTaskSessionFactory(builder, []TaskObjective{{Name: "collect", Objective: "Collect sources"}})
	if err != nil {
		t.Fatalf("NewModelTaskSessionFactory() error = %v", err)
	}
	tool := &capturingTool{name: "plugins_search"}
	session, err := factory.NewTaskSession(context.Background(), Dispatch{
		RunID: "run-1", TaskName: "collect", AttemptID: "run-1:collect:1",
		Grant: ExecutionGrant{TaskName: "collect", Model: "fast", Tools: []string{"plugins.search"}, RunAs: "user:one", InputsJSON: `{"topic":"runtime"}`, ConfigurationVersion: "v1"},
	}, map[string]aitools.Tool{"plugins.search": tool})
	if err != nil {
		t.Fatalf("NewTaskSession() error = %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Summary != "done" || result.Status != ResultSucceeded || len(result.RawOutput) == 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(modelSession.tools) != 1 || modelSession.tools[0].Name != "plugins_search" {
		t.Fatalf("model tools = %#v", modelSession.tools)
	}
	if tool.input != `{"query":"durability"}` {
		t.Fatalf("tool input = %q", tool.input)
	}
	wantKey := aitools.StableIdempotencyKey("run-1", "run-1:collect:1", "call-1")
	if tool.metadata.ToolUseID != "call-1" || tool.metadata.IdempotencyKey != wantKey {
		t.Fatalf("tool invocation metadata = %#v", tool.metadata)
	}
	if len(modelSession.results) != 1 || modelSession.results[0].ToolUseID != "call-1" {
		t.Fatalf("tool results = %#v", modelSession.results)
	}
	if builder.dispatch.Grant.Model != "fast" || builder.dispatch.Grant.RunAs != "user:one" {
		t.Fatalf("builder dispatch = %#v", builder.dispatch)
	}
}

func TestModelTaskSessionParksHumanInputAndReplaysOriginalToolCall(t *testing.T) {
	toolUse := &llm.ChatResponse{ContentBlocks: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{
		ID: "ask-1", Name: "builtins_human_ask", Input: json.RawMessage(`{"question":"Proceed?","timeout_seconds":120}`),
	}}}}
	bridge := &deferredHumanBridge{pending: true}
	tool := &aitools.HumanInputTool{Bridge: bridge}
	dispatch := Dispatch{
		RunID: "run-human", TaskName: "work", AttemptID: "run-human:work:1",
		Grant: ExecutionGrant{TaskName: "work", Model: "fast", Tools: []string{"builtins.human.ask"}, InputsJSON: `{}`},
	}
	firstModel := &fakeTaskModelSession{responses: []*llm.ChatResponse{toolUse}}
	factory, err := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: firstModel}, []TaskObjective{{Name: "work", Objective: "Ask first"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := factory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{"builtins.human.ask": tool})
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint TaskAttemptProgress
	_, err = first.(ResumableTaskSession).RunResumable(context.Background(), TaskAttemptProgress{
		AttemptID: dispatch.AttemptID, Phase: "dispatched", Tools: map[string]ToolOperation{},
	}, func(next TaskAttemptProgress) error {
		checkpoint = cloneTaskProgress(next)
		return nil
	})
	var waiting *TaskHumanInputWaitingError
	if !errors.As(err, &waiting) {
		t.Fatalf("RunResumable() error = %v, want human input wait", err)
	}
	wait := waiting.Wait
	wantID := aitools.StableIdempotencyKey(dispatch.RunID, dispatch.AttemptID, "ask-1")
	if wait.ToolCallID != wantID || wait.Question != "Proceed?" || wait.DeadlineAt == nil || wait.RequestedAt.IsZero() {
		t.Fatalf("wait = %#v", wait)
	}
	if checkpoint.Tools["ask-1"].Status != ToolOperationInFlight || len(firstModel.results) != 0 {
		t.Fatalf("pending tool leaked model result: progress=%#v results=%#v", checkpoint, firstModel.results)
	}
	if bridge.request.MissionID != dispatch.RunID || bridge.request.ToolCallID != wantID {
		t.Fatalf("bridge request = %#v", bridge.request)
	}
	bridge.pending = false
	secondModel := &fakeTaskModelSession{responses: []*llm.ChatResponse{{Content: "work finished"}}}
	resumeFactory, _ := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: secondModel}, []TaskObjective{{Name: "work", Objective: "Ask first"}})
	second, _ := resumeFactory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{"builtins.human.ask": tool})
	result, err := second.(ResumableTaskSession).RunResumable(context.Background(), checkpoint, func(next TaskAttemptProgress) error {
		checkpoint = cloneTaskProgress(next)
		return nil
	})
	if err != nil {
		t.Fatalf("resumed RunResumable() error = %v", err)
	}
	if result.Summary != "work finished" || bridge.calls != 2 || bridge.request.ToolCallID != wantID || checkpoint.Tools["ask-1"].Result != "yes" {
		t.Fatalf("resumed result/checkpoint/bridge = %#v / %#v / %#v", result, checkpoint, bridge)
	}
}

func TestTaskDeadlineBlocksUncertainNonReplaySafeTool(t *testing.T) {
	modelSession := &fakeTaskModelSession{responses: []*llm.ChatResponse{{ContentBlocks: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{
		ID: "charge-1", Name: "charge", Input: json.RawMessage(`{}`),
	}}}}}}
	factory, err := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{Name: "work", Objective: "Charge once"}})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := Dispatch{RunID: "run-charge", TaskName: "work", AttemptID: "run-charge:work:1", Grant: ExecutionGrant{TaskName: "work", Model: "fast"}}
	session, err := factory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{"charge": &deadlineBlockingTool{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var checkpoint TaskAttemptProgress
	_, err = session.(ResumableTaskSession).RunResumable(ctx, TaskAttemptProgress{AttemptID: dispatch.AttemptID, Phase: "dispatched"}, func(next TaskAttemptProgress) error {
		checkpoint = cloneTaskProgress(next)
		return nil
	})
	var blocked *TaskRecoveryBlockedError
	if !errors.As(err, &blocked) || blocked.ToolUseID != "charge-1" || blocked.IdempotencyKey == "" {
		t.Fatalf("RunResumable() error = %v, want uncertain external outcome", err)
	}
	if checkpoint.Tools["charge-1"].Status != ToolOperationInFlight || len(modelSession.results) != 0 {
		t.Fatalf("timed-out tool became a model-visible result: progress=%#v results=%#v", checkpoint, modelSession.results)
	}
}

type deadlineBlockingTool struct{}

func (*deadlineBlockingTool) ToolName() string        { return "charge" }
func (*deadlineBlockingTool) ToolDescription() string { return "charge" }
func (*deadlineBlockingTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{Type: aitools.TypeObject}
}
func (*deadlineBlockingTool) Call(ctx context.Context, _ string) string {
	<-ctx.Done()
	return "unknown external outcome"
}

type deferredHumanBridge struct {
	pending bool
	calls   int
	request aitools.HumanInputRequest
}

func (*deferredHumanBridge) AskHuman(context.Context, aitools.HumanInputRequest) (string, error) {
	return "", fmt.Errorf("blocking AskHuman must not be called by a mission task")
}

func (*deferredHumanBridge) HumanInputReplaySafe() bool { return true }

func (b *deferredHumanBridge) BeginAskHuman(_ context.Context, request aitools.HumanInputRequest) (aitools.DeferredHumanInputResult, error) {
	b.calls++
	b.request = request
	return aitools.DeferredHumanInputResult{Pending: b.pending, Response: "yes", RequestedAt: time.Now().UTC()}, nil
}

func TestIterationChildPromptReceivesOnlyItsFrozenItem(t *testing.T) {
	modelSession := &fakeTaskModelSession{responses: []*llm.ChatResponse{{Content: "done"}}}
	factory, err := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{Name: "work", Objective: "Process one item"}})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := Dispatch{RunID: "run-iteration", TaskName: "work[1]", AttemptID: "run-iteration:work[1]:1", Grant: ExecutionGrant{
		TaskName: "work[1]", Model: "fast", InputsJSON: `{"mission":"input"}`,
		Iteration: &IterationInput{ParentTask: "work", Index: 1, Item: json.RawMessage(`{"name":"second"}`)},
	}}
	session, err := factory.NewTaskSession(context.Background(), dispatch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(modelSession.prompts) != 1 || !strings.Contains(modelSession.prompts[0], `"name":"second"`) || !strings.Contains(modelSession.prompts[0], `"parentTask":"work"`) {
		t.Fatalf("iteration prompt = %v", modelSession.prompts)
	}
}

func TestTaskPromptReceivesAcceptedDependencyAggregate(t *testing.T) {
	modelSession := &fakeTaskModelSession{responses: []*llm.ChatResponse{{Content: "analyzed"}}}
	factory, err := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{Name: "analyze", Objective: "Analyze the completed work"}})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := Dispatch{RunID: "run-aggregate", TaskName: "analyze", AttemptID: "run-aggregate:analyze:1", Grant: ExecutionGrant{
		TaskName: "analyze", Model: "fast", Dependencies: []DependencySnapshot{{
			TaskName: "work", Status: ResultSucceeded, Summary: "Completed 2 iteration items",
			Data: json.RawMessage(`{"total":2,"items":[{"index":0,"taskName":"work[0]"},{"index":1,"taskName":"work[1]"}]}`),
		}},
	}}
	session, err := factory.NewTaskSession(context.Background(), dispatch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(modelSession.prompts) != 1 || !strings.Contains(modelSession.prompts[0], `"dependencies"`) || !strings.Contains(modelSession.prompts[0], `"taskName":"work[1]"`) {
		t.Fatalf("dependency prompt = %v", modelSession.prompts)
	}
}

func TestModelTaskSessionRequestsAndReturnsTypedData(t *testing.T) {
	t.Parallel()

	modelSession := &fakeTaskModelSession{responses: []*llm.ChatResponse{{Content: `{"answer":"forty-two"}`}}}
	factory, err := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{
		Name: "answer", Objective: "Answer the question",
		ResultPolicy: ResultPolicy{Fields: []ResultField{{Name: "answer", Type: "string", Required: true}}},
	}})
	if err != nil {
		t.Fatalf("NewModelTaskSessionFactory() error = %v", err)
	}
	dispatch := Dispatch{
		RunID: "run-1", TaskName: "answer", AttemptID: "run-1:answer:1",
		Grant: ExecutionGrant{TaskName: "answer", Model: "fast", RunAs: "user:one", ConfigurationVersion: "v1"},
	}
	session, err := factory.NewTaskSession(context.Background(), dispatch, nil)
	if err != nil {
		t.Fatalf("NewTaskSession() error = %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if string(result.Data) != `{"answer":"forty-two"}` {
		t.Fatalf("result data = %s", result.Data)
	}
	if len(modelSession.prompts) != 1 || !strings.Contains(modelSession.prompts[0], "outputContract") || !strings.Contains(modelSession.prompts[0], "Return only one JSON object") {
		t.Fatalf("task prompt = %q", modelSession.prompts)
	}
}

func TestModelTaskSessionReceivesRouteCriteriaWithoutRouteAuthority(t *testing.T) {
	t.Parallel()
	modelSession := &fakeTaskModelSession{responses: []*llm.ChatResponse{{Content: "The request concerns technical support."}}}
	factory, err := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{
		Name: "classify", Objective: "Classify the request",
		Routes: []RouteDefinition{{Target: "support", Condition: "Technical support request"}},
	}})
	if err != nil {
		t.Fatalf("NewModelTaskSessionFactory() error = %v", err)
	}
	session, err := factory.NewTaskSession(context.Background(), Dispatch{
		RunID: "run-route", TaskName: "classify", AttemptID: "run-route:classify:1",
		Grant: ExecutionGrant{TaskName: "classify", Model: "fast", RunAs: "user:one", ConfigurationVersion: "v1"},
	}, nil)
	if err != nil {
		t.Fatalf("NewTaskSession() error = %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(modelSession.prompts) != 1 || !strings.Contains(modelSession.prompts[0], "routingCriteria") || !strings.Contains(modelSession.prompts[0], "Do not choose a route") {
		t.Fatalf("route-aware task prompt = %q", modelSession.prompts)
	}
	for _, tool := range modelSession.tools {
		if tool.Name == "select_route" || tool.Name == "inspect_task" {
			t.Fatalf("task worker received commander control: %#v", tool)
		}
	}
}

func TestModelTaskSessionClarificationRestoresTranscriptAndDisablesTools(t *testing.T) {
	t.Parallel()
	modelSession := &fakeTaskModelSession{responses: []*llm.ChatResponse{{Content: "The decisive evidence was the HTTP 401 response."}}}
	factory, err := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{Name: "classify", Objective: "Classify the request"}})
	if err != nil {
		t.Fatalf("NewModelTaskSessionFactory() error = %v", err)
	}
	transcript := []TaskSessionMessage{
		{Role: string(llm.RoleUser), Parts: []store.MessagePart{{Type: "text", Text: "original task"}}},
		{Role: string(llm.RoleAssistant), Parts: []store.MessagePart{{Type: "text", Text: "It appears to be an authentication problem."}}},
	}
	result, err := factory.Clarify(context.Background(), Dispatch{TaskName: "classify"}, transcript, "What evidence makes this an authentication problem?")
	if err != nil {
		t.Fatalf("Clarify() error = %v", err)
	}
	if len(modelSession.tools) != 0 {
		t.Fatalf("clarification tools = %#v, want none", modelSession.tools)
	}
	if len(result.Answer) != 1 || !strings.Contains(result.Answer[0].Text, "401") {
		t.Fatalf("clarification answer = %#v", result.Answer)
	}
	if len(result.SessionTranscript) != 4 {
		t.Fatalf("updated transcript length = %d, want 4", len(result.SessionTranscript))
	}
	if len(modelSession.prompts) != 1 || !strings.Contains(modelSession.prompts[0], "Do not perform new work") {
		t.Fatalf("clarification prompt = %#v", modelSession.prompts)
	}
}

func TestModelTaskSessionResumesCommittedToolWithoutInvokingItAgain(t *testing.T) {
	t.Parallel()
	toolUse := &llm.ToolUseBlock{ID: "call-1", Name: "search", Input: json.RawMessage(`{"query":"durability"}`)}
	transcript := []TaskSessionMessage{
		{Role: string(llm.RoleUser), Parts: []store.MessagePart{{Type: "text", Text: "collect evidence"}}},
		{Role: string(llm.RoleAssistant), Parts: agent.PartsFromMessage(llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ToolUse: toolUse}}})},
	}
	modelSession := &fakeTaskModelSession{responses: []*llm.ChatResponse{{Content: "finished from restored tool result"}}}
	tool := &countingTool{name: "search"}
	factory, err := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{Name: "collect", Objective: "Collect evidence"}})
	if err != nil {
		t.Fatalf("NewModelTaskSessionFactory() error = %v", err)
	}
	dispatch := Dispatch{RunID: "run-resume", TaskName: "collect", AttemptID: "run-resume:collect:1", Grant: ExecutionGrant{TaskName: "collect", Model: "fast", RunAs: "user:one", ConfigurationVersion: "v1"}}
	session, err := factory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{"search": tool})
	if err != nil {
		t.Fatalf("NewTaskSession() error = %v", err)
	}
	progress := TaskAttemptProgress{AttemptID: dispatch.AttemptID, Phase: "tool_calls_received", Transcript: transcript, Tools: map[string]ToolOperation{
		"call-1": {ToolUseID: "call-1", ToolName: "search", InputJSON: `{"query":"durability"}`, Status: ToolOperationCompleted, Result: "persisted result"},
	}}
	var phases []string
	result, err := session.(ResumableTaskSession).RunResumable(context.Background(), progress, func(next TaskAttemptProgress) error {
		phases = append(phases, next.Phase)
		return nil
	})
	if err != nil {
		t.Fatalf("RunResumable() error = %v", err)
	}
	if tool.calls != 0 {
		t.Fatalf("restored completed tool invoked %d times", tool.calls)
	}
	if result.Summary != "finished from restored tool result" {
		t.Fatalf("result = %#v", result)
	}
	if len(modelSession.results) != 1 || modelSession.results[0].Content != "persisted result" {
		t.Fatalf("restored tool results = %#v", modelSession.results)
	}
	if strings.Join(phases, ",") != "tool_calls_received,tool_results_added,final_response" {
		t.Fatalf("checkpoint phases = %v", phases)
	}
}

func TestModelTaskSessionBlocksUnknownInFlightToolOutcome(t *testing.T) {
	t.Parallel()
	toolUse := &llm.ToolUseBlock{ID: "call-uncertain", Name: "search", Input: json.RawMessage(`{}`)}
	transcript := []TaskSessionMessage{{Role: string(llm.RoleAssistant), Parts: agent.PartsFromMessage(llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ToolUse: toolUse}}})}}
	modelSession := &fakeTaskModelSession{}
	tool := &countingTool{name: "search"}
	factory, _ := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{Name: "collect", Objective: "Collect evidence"}})
	dispatch := Dispatch{TaskName: "collect", AttemptID: "attempt-1", Grant: ExecutionGrant{TaskName: "collect"}}
	session, _ := factory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{"search": tool})
	_, err := session.(ResumableTaskSession).RunResumable(context.Background(), TaskAttemptProgress{
		AttemptID: "attempt-1", Phase: "tool_calls_received", Transcript: transcript,
		Tools: map[string]ToolOperation{"call-uncertain": {ToolUseID: "call-uncertain", ToolName: "search", InputJSON: `{}`, Status: ToolOperationInFlight}},
	}, func(TaskAttemptProgress) error { return nil })
	var blocked *TaskRecoveryBlockedError
	if !errors.As(err, &blocked) || !strings.Contains(err.Error(), "uncertain external outcome") {
		t.Fatalf("RunResumable() error = %v", err)
	}
	if tool.calls != 0 {
		t.Fatalf("uncertain tool was replayed %d times", tool.calls)
	}
}

func TestModelTaskSessionReplaysDeclaredIdempotentInFlightTool(t *testing.T) {
	t.Parallel()
	toolUse := &llm.ToolUseBlock{ID: "call-safe", Name: "lookup", Input: json.RawMessage(`{"id":"one"}`)}
	transcript := []TaskSessionMessage{{Role: string(llm.RoleAssistant), Parts: agent.PartsFromMessage(llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ToolUse: toolUse}}})}}
	modelSession := &fakeTaskModelSession{responses: []*llm.ChatResponse{{Content: "done"}}}
	tool := &replaySafeCountingTool{countingTool: countingTool{name: "lookup"}}
	factory, _ := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{Name: "collect", Objective: "Collect evidence"}})
	dispatch := Dispatch{RunID: "run-safe", TaskName: "collect", AttemptID: "attempt-safe", Grant: ExecutionGrant{TaskName: "collect"}}
	session, _ := factory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{"lookup": tool})
	var replayCommitted bool
	result, err := session.(ResumableTaskSession).RunResumable(context.Background(), TaskAttemptProgress{
		AttemptID: dispatch.AttemptID, Phase: "tool_calls_received", Transcript: transcript,
		Tools: map[string]ToolOperation{"call-safe": {ToolUseID: "call-safe", ToolName: "lookup", InputJSON: `{"id":"one"}`, Status: ToolOperationInFlight}},
	}, func(progress TaskAttemptProgress) error {
		if operation := progress.Tools["call-safe"]; operation.Status == ToolOperationInFlight && operation.ReplayCount == 1 && tool.calls == 0 {
			replayCommitted = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunResumable() error = %v", err)
	}
	if result.Summary != "done" || tool.calls != 1 || !replayCommitted {
		t.Fatalf("result/calls = %#v/%d", result, tool.calls)
	}
	if tool.metadata.IdempotencyKey != aitools.StableIdempotencyKey("run-safe", "attempt-safe", "call-safe") {
		t.Fatalf("replayed tool metadata = %#v", tool.metadata)
	}
}

func TestModelTaskSessionCancellationLeavesReplaySafeToolInFlightForRestart(t *testing.T) {
	toolUse := &llm.ToolUseBlock{ID: "call-wait", Name: "wait_for_human", Input: json.RawMessage(`{"question":"continue?"}`)}
	firstModel := &fakeTaskModelSession{responses: []*llm.ChatResponse{{ContentBlocks: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ToolUse: toolUse}}}}}
	blocking := &cancelBlockingReplaySafeTool{started: make(chan struct{})}
	factory, _ := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: firstModel}, []TaskObjective{{Name: "work", Objective: "Do work"}})
	dispatch := Dispatch{RunID: "run-wait", TaskName: "work", AttemptID: "attempt-wait", Grant: ExecutionGrant{TaskName: "work"}}
	first, _ := factory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{"wait_for_human": blocking})
	ctx, cancel := context.WithCancel(context.Background())
	var checkpoint TaskAttemptProgress
	done := make(chan error, 1)
	go func() {
		_, err := first.(ResumableTaskSession).RunResumable(ctx, TaskAttemptProgress{AttemptID: dispatch.AttemptID, Phase: "dispatched", Tools: map[string]ToolOperation{}}, func(next TaskAttemptProgress) error {
			checkpoint = cloneTaskProgress(next)
			return nil
		})
		done <- err
	}()
	<-blocking.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("RunResumable() error = %v, want context canceled", err)
	}
	if operation := checkpoint.Tools["call-wait"]; operation.Status != ToolOperationInFlight {
		t.Fatalf("canceled tool operation = %#v, want in_flight", operation)
	}

	secondModel := &fakeTaskModelSession{responses: []*llm.ChatResponse{{Content: "continued after answer"}}}
	replayed := &replaySafeCountingTool{countingTool: countingTool{name: "wait_for_human"}}
	resumeFactory, _ := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: secondModel}, []TaskObjective{{Name: "work", Objective: "Do work"}})
	second, _ := resumeFactory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{"wait_for_human": replayed})
	result, err := second.(ResumableTaskSession).RunResumable(context.Background(), checkpoint, func(TaskAttemptProgress) error { return nil })
	if err != nil {
		t.Fatalf("resumed RunResumable() error = %v", err)
	}
	if result.Summary != "continued after answer" || replayed.calls != 1 {
		t.Fatalf("resumed result/calls = %#v / %d", result, replayed.calls)
	}
}

func TestModelTaskSessionReturnsCommittedFinalResponseWithoutAnotherProviderCall(t *testing.T) {
	t.Parallel()
	modelSession := &fakeTaskModelSession{}
	factory, _ := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: modelSession}, []TaskObjective{{Name: "work", Objective: "Do work"}})
	dispatch := Dispatch{RunID: "run-final", TaskName: "work", AttemptID: "run-final:work:1", Grant: ExecutionGrant{TaskName: "work", Model: "fast", RunAs: "user:one", ConfigurationVersion: "v1"}}
	session, _ := factory.NewTaskSession(context.Background(), dispatch, nil)
	transcript := []TaskSessionMessage{{Role: string(llm.RoleAssistant), Parts: []store.MessagePart{{Type: "text", Text: "already complete"}}}}
	result, err := session.(ResumableTaskSession).RunResumable(context.Background(), TaskAttemptProgress{
		AttemptID: dispatch.AttemptID, Phase: "final_response", Transcript: transcript, Tools: map[string]ToolOperation{},
	}, func(TaskAttemptProgress) error { return nil })
	if err != nil {
		t.Fatalf("RunResumable() error = %v", err)
	}
	if result.Summary != "already complete" {
		t.Fatalf("result = %#v", result)
	}
	if len(modelSession.prompts) != 0 || modelSession.index != 0 {
		t.Fatalf("provider was called again: prompts=%#v index=%d", modelSession.prompts, modelSession.index)
	}
}

func TestModelTaskSessionPersistsStableTelemetryAcrossResume(t *testing.T) {
	t.Parallel()
	toolUse := &llm.ToolUseBlock{ID: "call-telemetry", Name: "lookup", Input: json.RawMessage(`{"id":"one"}`)}
	firstModel := &fakeTaskModelSession{responses: []*llm.ChatResponse{
		{ID: "provider-turn-1", FinishReason: "tool_use", Usage: llm.Usage{InputTokens: 10, OutputTokens: 2}, ContentBlocks: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ToolUse: toolUse}}},
		{ID: "provider-turn-2", FinishReason: "end_turn", Usage: llm.Usage{InputTokens: 14, OutputTokens: 5, CacheReadTokens: 7}, Content: "done"},
	}}
	factory, _ := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: firstModel}, []TaskObjective{{Name: "work", Objective: "Do work"}})
	dispatch := Dispatch{RunID: "run-telemetry", TaskName: "work", AttemptID: "run-telemetry:work:1", Grant: ExecutionGrant{TaskName: "work", Model: "fast", RunAs: "user:one", ConfigurationVersion: "v1"}}
	session, _ := factory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{"lookup": &countingTool{name: "lookup"}})
	var checkpoint TaskAttemptProgress
	result, err := session.(ResumableTaskSession).RunResumable(context.Background(), TaskAttemptProgress{
		AttemptID: dispatch.AttemptID, Phase: "dispatched", Tools: map[string]ToolOperation{},
	}, func(next TaskAttemptProgress) error {
		checkpoint = cloneTaskProgress(next)
		return nil
	})
	if err != nil {
		t.Fatalf("RunResumable() error = %v", err)
	}
	if result.Summary != "done" || checkpoint.Phase != "final_response" {
		t.Fatalf("result/checkpoint = %#v / %#v", result, checkpoint)
	}
	var turns []ModelTurnTelemetry
	for _, message := range checkpoint.Transcript {
		if message.Telemetry != nil {
			turns = append(turns, *message.Telemetry)
		}
	}
	if len(turns) != 2 || turns[0].ID != dispatch.AttemptID+":turn:1:provider-turn-1" || turns[1].ID != dispatch.AttemptID+":turn:2:provider-turn-2" {
		t.Fatalf("persisted turns = %#v", turns)
	}
	if turns[0].InputTokens != 10 || turns[1].OutputTokens != 5 || turns[1].CacheReadTokens != 7 {
		t.Fatalf("persisted usage = %#v", turns)
	}

	restoredModel := &fakeTaskModelSession{}
	restoredFactory, _ := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: restoredModel}, []TaskObjective{{Name: "work", Objective: "Do work"}})
	restoredSession, _ := restoredFactory.NewTaskSession(context.Background(), dispatch, nil)
	replayed, err := restoredSession.(ResumableTaskSession).RunResumable(context.Background(), checkpoint, func(TaskAttemptProgress) error { return nil })
	if err != nil {
		t.Fatalf("restored RunResumable() error = %v", err)
	}
	if replayed.Summary != "done" || restoredModel.index != 0 || len(restoredModel.prompts) != 0 {
		t.Fatalf("restored result/provider calls = %#v / %d / %#v", replayed, restoredModel.index, restoredModel.prompts)
	}
}

type fakeTaskModelSessionBuilder struct {
	session  TaskModelSession
	dispatch Dispatch
}

func (b *fakeTaskModelSessionBuilder) BuildTaskModelSession(_ context.Context, dispatch Dispatch) (TaskModelSession, error) {
	b.dispatch = dispatch
	return b.session, nil
}

type fakeTaskModelSession struct {
	tools     []llm.ToolDefinition
	responses []*llm.ChatResponse
	results   []llm.ToolResultBlock
	index     int
	prompts   []string
	messages  []llm.Message
}

func (s *fakeTaskModelSession) SetTools(tools []llm.ToolDefinition) { s.tools = tools }
func (s *fakeTaskModelSession) LoadMessages(messages []llm.Message) {
	s.messages = append([]llm.Message(nil), messages...)
}
func (s *fakeTaskModelSession) SnapshotMessages() []llm.Message {
	return append([]llm.Message(nil), s.messages...)
}
func (s *fakeTaskModelSession) AddToolResults(results []llm.ToolResultBlock) {
	s.results = append(s.results, results...)
	parts := make([]llm.ContentBlock, len(results))
	for index := range results {
		result := results[index]
		parts[index] = llm.ContentBlock{Type: llm.ContentTypeToolResult, ToolResult: &result}
	}
	s.messages = append(s.messages, llm.Message{Role: llm.RoleUser, Parts: parts})
}

type countingTool struct {
	name  string
	calls int
}

func (t *countingTool) ToolName() string        { return t.name }
func (t *countingTool) ToolDescription() string { return t.name }
func (*countingTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{Type: aitools.TypeObject}
}
func (t *countingTool) Call(context.Context, string) string {
	t.calls++
	return "fresh result"
}

type replaySafeCountingTool struct {
	countingTool
	metadata aitools.InvocationMetadata
}

type cancelBlockingReplaySafeTool struct{ started chan struct{} }

func (*cancelBlockingReplaySafeTool) ToolName() string        { return "wait_for_human" }
func (*cancelBlockingReplaySafeTool) ToolDescription() string { return "wait" }
func (*cancelBlockingReplaySafeTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{Type: aitools.TypeObject}
}
func (*cancelBlockingReplaySafeTool) ToolReplaySafe() bool { return true }
func (t *cancelBlockingReplaySafeTool) Call(ctx context.Context, _ string) string {
	close(t.started)
	<-ctx.Done()
	return "synthetic cancellation must not be committed"
}

func (*replaySafeCountingTool) ToolReplaySafe() bool { return true }
func (t *replaySafeCountingTool) Call(ctx context.Context, input string) string {
	t.metadata, _ = aitools.InvocationMetadataFromContext(ctx)
	return t.countingTool.Call(ctx, input)
}
func (s *fakeTaskModelSession) SendStream(_ context.Context, prompt string, _ func(llm.StreamChunk)) (*llm.ChatResponse, error) {
	s.prompts = append(s.prompts, prompt)
	s.messages = append(s.messages, llm.NewTextMessage(llm.RoleUser, prompt))
	response := s.responses[s.index]
	s.index++
	s.messages = append(s.messages, llm.Message{Role: llm.RoleAssistant, Content: response.Content, Parts: response.ContentBlocks})
	return response, nil
}
func (s *fakeTaskModelSession) ContinueStream(context.Context, func(llm.StreamChunk)) (*llm.ChatResponse, error) {
	response := s.responses[s.index]
	s.index++
	s.messages = append(s.messages, llm.Message{Role: llm.RoleAssistant, Content: response.Content, Parts: response.ContentBlocks})
	return response, nil
}
