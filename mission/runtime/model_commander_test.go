package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"squadron/llm"
	"squadron/store"
)

func TestModelCommanderExposesOnlyStartTaskAndRestoresTranscript(t *testing.T) {
	t.Parallel()

	session := &fakeCommanderSession{response: &llm.ChatResponse{
		ID: "response-1", FinishReason: "tool_use", Usage: llm.Usage{InputTokens: 12, OutputTokens: 4, CacheReadTokens: 3},
		ContentBlocks: []llm.ContentBlock{{
			Type:    llm.ContentTypeToolUse,
			ToolUse: &llm.ToolUseBlock{ID: "tool-1", Name: "start_task", Input: json.RawMessage(`{"task_name":"collect","model":"fast"}`)},
		}},
	}}
	restored := []CommanderMessage{{ID: "old", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: "prior"}}}}
	commander, err := NewModelCommander(session, restored)
	if err != nil {
		t.Fatalf("NewModelCommander() error = %v", err)
	}
	if len(session.tools) != 4 || session.tools[0].Name != "start_task" || session.tools[1].Name != "select_route" || session.tools[2].Name != "inspect_task" || session.tools[3].Name != "clarify_task_result" {
		t.Fatalf("commander tools = %#v", session.tools)
	}
	if len(session.loaded) != 1 || session.loaded[0].GetTextContent() != "prior" {
		t.Fatalf("restored messages = %#v", session.loaded)
	}

	decision, err := commander.Decide(context.Background(), Snapshot{
		RunID: "run-1", Revision: 4, Ready: []string{"collect"},
		Tasks: map[string]TaskSnapshot{"collect": {Name: "collect", State: TaskReady}},
	}, restored)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Dispatch.TaskName != "collect" || decision.Dispatch.RequestID != "tool-1" || decision.Dispatch.ReadySetRevision != 4 {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.Grant.TaskName != "collect" || decision.Grant.Model != "fast" {
		t.Fatalf("grant request = %#v", decision.Grant)
	}
	if len(decision.Message.Parts) != 1 || decision.Message.Parts[0].ToolName != "start_task" {
		t.Fatalf("assistant message = %#v", decision.Message)
	}
	if turn := decision.Message.Telemetry; turn == nil || turn.ID != "commander:turn:1:response-1" || turn.InputTokens != 12 || turn.OutputTokens != 4 || turn.CacheReadTokens != 3 {
		t.Fatalf("commander telemetry = %#v", turn)
	}

	commander.DispatchAccepted(Dispatch{RequestID: "tool-1", AttemptID: "run-1:collect:1", TaskName: "collect"})
	if len(session.results) != 1 || session.results[0].ToolUseID != "tool-1" {
		t.Fatalf("tool results = %#v", session.results)
	}
	nextHistory := append(append(append([]CommanderMessage(nil), restored...), decision.Prompt, decision.Message),
		CommanderMessage{ID: "response-1:result", Role: "user", Parts: []store.MessagePart{{Type: "tool_result", ToolUseID: "tool-1", Text: `{"status":"started"}`}}})
	if _, err := commander.Decide(context.Background(), Snapshot{
		RunID: "run-1", Revision: 5, Ready: []string{"collect"},
		Tasks: map[string]TaskSnapshot{"collect": {Name: "collect", State: TaskReady}},
	}, nextHistory); err != nil {
		t.Fatalf("next Decide() error = %v", err)
	}
	if len(session.loaded) != len(nextHistory) || session.loaded[len(session.loaded)-1].Role != llm.RoleUser {
		t.Fatalf("accepted tool result was not restored before the next turn: %#v", session.loaded)
	}
}

func TestCommanderContextWindowPreservesWholeRecentExchangesAcrossRestart(t *testing.T) {
	t.Parallel()
	var history []CommanderMessage
	for i := 0; i < 15; i++ {
		prefix := fmt.Sprintf("turn-%02d", i)
		history = append(history,
			CommanderMessage{ID: prefix + ":prompt", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: prefix}}},
			CommanderMessage{ID: prefix, Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: prefix, ToolName: "start_task", ToolInputJSON: `{}`}}},
			CommanderMessage{ID: prefix + ":result", Role: "user", Parts: []store.MessagePart{{Type: "tool_result", ToolUseID: prefix, Text: `{}`}}},
		)
	}
	window := commanderContextWindow(history)
	if len(window) != commanderHistoryMaxMessages || window[0].ID != "turn-07:prompt" || window[len(window)-1].ID != "turn-14:result" {
		t.Fatalf("window selected incomplete exchanges: first=%q last=%q count=%d", window[0].ID, window[len(window)-1].ID, len(window))
	}
	first := &fakeCommanderSession{}
	if _, err := NewModelCommander(first, history); err != nil {
		t.Fatal(err)
	}
	restarted := &fakeCommanderSession{}
	if _, err := NewModelCommander(restarted, history); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.loaded, restarted.loaded) || len(first.loaded) != len(window) {
		t.Fatal("commander context changed after rehydration")
	}
}

func TestCommanderContextWindowLimitsOlderLargeResults(t *testing.T) {
	t.Parallel()
	var history []CommanderMessage
	for i := 0; i < 10; i++ {
		prefix := fmt.Sprintf("turn-%02d", i)
		history = append(history,
			CommanderMessage{ID: prefix + ":prompt", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: prefix}}},
			CommanderMessage{ID: prefix, Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: prefix, ToolName: "inspect_task", ToolInputJSON: `{}`}}},
			CommanderMessage{ID: prefix + ":result", Role: "user", Parts: []store.MessagePart{{Type: "tool_result", ToolUseID: prefix, Text: strings.Repeat("x", 20*1024)}}},
		)
	}
	window := commanderContextWindow(history)
	encoded, err := json.Marshal(window)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > commanderHistoryMaxBytes || len(window) == len(history) || !strings.HasSuffix(window[0].ID, ":prompt") || window[len(window)-1].ID != "turn-09:result" {
		t.Fatalf("large-result context was not bounded at exchange boundaries: bytes=%d count=%d", len(encoded), len(window))
	}
}

func TestCommanderPromptProjectionBoundsGrowingIterationState(t *testing.T) {
	t.Parallel()
	snapshot := Snapshot{
		RunID: "run-1", Revision: 17, Tasks: map[string]TaskSnapshot{},
		Results: map[string]TaskResultSummary{}, PendingRoutes: map[string][]RouteDefinition{},
	}
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("item[%03d]", i)
		snapshot.Ready = append(snapshot.Ready, name)
		snapshot.Tasks[name] = TaskSnapshot{Name: name, State: TaskReady}
		snapshot.Results[name] = TaskResultSummary{Status: ResultSucceeded, Summary: strings.Repeat("a", 4000)}
	}
	snapshot.PendingRoutes["item[099]"] = []RouteDefinition{{Target: "finish", Condition: "done"}}
	encoded, err := json.Marshal(commanderPromptProjection(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		Ready                []string                     `json:"ready_tasks"`
		ReadyOmitted         int                          `json:"ready_tasks_omitted"`
		TaskStateCounts      map[TaskState]int            `json:"task_state_counts"`
		Results              map[string]TaskResultSummary `json:"results"`
		ResultsOmitted       int                          `json:"results_omitted"`
		PendingRoutes        map[string][]RouteDefinition `json:"pending_routes"`
		PendingRoutesOmitted int                          `json:"pending_routes_omitted"`
	}
	if err := json.Unmarshal(encoded, &projection); err != nil {
		t.Fatal(err)
	}
	if len(projection.Ready) != commanderReadyLimit || projection.ReadyOmitted != 68 || projection.TaskStateCounts[TaskReady] != 100 {
		t.Fatalf("ready projection = %+v", projection)
	}
	if len(projection.Results) != commanderResultLimit || projection.ResultsOmitted != 76 {
		t.Fatalf("result projection count = %d, omitted = %d", len(projection.Results), projection.ResultsOmitted)
	}
	if _, ok := projection.Results["item[099]"]; !ok {
		t.Fatal("pending router result was omitted")
	}
	if len([]rune(projection.Results["item[099]"].Summary)) > commanderSummaryRuneLimit+40 {
		t.Fatal("result summary was not truncated")
	}
	if len(projection.PendingRoutes) != 1 || projection.PendingRoutesOmitted != 0 || len(encoded) > 20*1024 {
		t.Fatalf("projection size = %d, routes = %d", len(encoded), len(projection.PendingRoutes))
	}
}

func TestCommanderSystemPromptDefinesGraphWithoutTaskCapabilities(t *testing.T) {
	t.Parallel()

	prompt, err := commanderSystemPrompt(CommanderBootstrap{
		MissionName: "research",
		Directive:   "Produce a concise report.",
		Tasks: []TaskDefinition{
			{Name: "collect", Objective: "Collect sources"},
			{Name: "write", Objective: "Write report", DependsOn: []string{"collect"}},
		},
		VisibleInputs:        map[string]any{"topic": "durability"},
		Policies:             map[string]any{"task_allowed_models": map[string][]string{"collect": {"claude_haiku_4_5"}}},
		ConfigurationVersion: "sha256:abc",
	})
	if err != nil {
		t.Fatalf("commanderSystemPrompt() error = %v", err)
	}
	for _, required := range []string{"single commander", "start_task", "collect", "write", "sha256:abc", "durability", "claude_haiku_4_5", "exact model key"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt does not contain %q: %s", required, prompt)
		}
	}
	if !strings.Contains(prompt, "cannot execute mission or task capabilities") {
		t.Fatalf("prompt does not state capability boundary: %s", prompt)
	}
}

func TestModelCommanderRetriesParallelControlCallsWithoutPersistingRejectedTurn(t *testing.T) {
	t.Parallel()
	session := &fakeCommanderSession{responses: []*llm.ChatResponse{
		{ID: "parallel", Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}, ContentBlocks: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "first", Name: "start_task", Input: json.RawMessage(`{"task_name":"audience","model":"fast"}`)}},
			{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "second", Name: "start_task", Input: json.RawMessage(`{"task_name":"benefit","model":"fast"}`)}},
		}},
		{ID: "single", Usage: llm.Usage{InputTokens: 12, OutputTokens: 3}, ContentBlocks: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "one", Name: "start_task", Input: json.RawMessage(`{"task_name":"audience","model":"fast"}`)}},
		}},
	}}
	commander, err := NewModelCommander(session, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := commander.Decide(context.Background(), Snapshot{RunID: "run-1", Revision: 1, Ready: []string{"audience", "benefit"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Dispatch.TaskName != "audience" || decision.Message.ID != "single" || len(decision.Message.Parts) != 1 {
		t.Fatalf("unexpected accepted decision: %#v", decision)
	}
	if decision.Message.Telemetry.InputTokens != 22 || decision.Message.Telemetry.OutputTokens != 8 {
		t.Fatalf("retry token accounting = %#v", decision.Message.Telemetry)
	}
	if len(session.prompts) != 2 || !strings.Contains(session.prompts[1], "exactly ONE") || session.loadCount != 3 {
		t.Fatalf("retry prompts = %#v, restored %d times", session.prompts, session.loadCount)
	}
}

func TestModelCommanderSelectsPendingDeclaredRoute(t *testing.T) {
	t.Parallel()
	session := &fakeCommanderSession{response: &llm.ChatResponse{ID: "route-response", ContentBlocks: []llm.ContentBlock{{
		Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "route-tool", Name: "select_route", Input: json.RawMessage(`{"task_name":"classify","target":"support"}`)},
	}}}}
	commander, err := NewModelCommander(session, nil)
	if err != nil {
		t.Fatalf("NewModelCommander() error = %v", err)
	}
	decision, err := commander.Decide(context.Background(), Snapshot{
		RunID: "run-route", Revision: 7,
		PendingRoutes: map[string][]RouteDefinition{"classify": {{Target: "support", Condition: "support"}}},
	}, nil)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Route.TaskName != "classify" || decision.Route.Target != "support" || decision.Route.Revision != 7 {
		t.Fatalf("route decision = %#v", decision.Route)
	}
	commander.RouteAccepted(RouteSelection{RequestID: "route-tool", TaskName: "classify", Target: "support"})
	if len(session.results) != 1 || session.results[0].ToolUseID != "route-tool" {
		t.Fatalf("route results = %#v", session.results)
	}
}

func TestModelCommanderNormalizesAndValidatesTypedMissionRouteInputs(t *testing.T) {
	t.Parallel()
	session := &fakeCommanderSession{response: &llm.ChatResponse{ID: "route-response", ContentBlocks: []llm.ContentBlock{{
		Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "route-tool", Name: "select_route", Input: json.RawMessage(`{"task_name":"classify","target":"follow_up","inputs":{"enabled":false,"attempt":2,"labels":["a","b"]}}`)},
	}}}}
	commander, err := NewModelCommander(session, nil)
	if err != nil {
		t.Fatalf("NewModelCommander() error = %v", err)
	}
	routes := []RouteDefinition{{
		Target: "follow_up", Condition: "follow up", IsMission: true,
		Inputs: []RouteInputDefinition{
			{Name: "enabled", Type: "bool", Required: true},
			{Name: "attempt", Type: "integer", Required: true},
			{Name: "labels", Type: "list"},
		},
	}}
	decision, err := commander.Decide(context.Background(), Snapshot{
		RunID: "run-route", Revision: 7, PendingRoutes: map[string][]RouteDefinition{"classify": routes},
	}, nil)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Route.Inputs["enabled"] != "false" || decision.Route.Inputs["attempt"] != "2" || decision.Route.Inputs["labels"] != `["a","b"]` {
		t.Fatalf("normalized route inputs = %#v", decision.Route.Inputs)
	}
}

func TestModelCommanderRejectsInvalidMissionRouteInputsTogether(t *testing.T) {
	t.Parallel()
	session := &fakeCommanderSession{response: &llm.ChatResponse{ID: "route-response", ContentBlocks: []llm.ContentBlock{{
		Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "route-tool", Name: "select_route", Input: json.RawMessage(`{"task_name":"classify","target":"follow_up","inputs":{"enabled":"maybe","extra":1}}`)},
	}}}}
	commander, err := NewModelCommander(session, nil)
	if err != nil {
		t.Fatalf("NewModelCommander() error = %v", err)
	}
	_, err = commander.Decide(context.Background(), Snapshot{
		RunID: "run-route", Revision: 7,
		PendingRoutes: map[string][]RouteDefinition{"classify": {{
			Target: "follow_up", IsMission: true,
			Inputs: []RouteInputDefinition{{Name: "enabled", Type: "bool", Required: true}, {Name: "attempt", Type: "integer", Required: true}},
		}}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "inputs.attempt is required") || !strings.Contains(err.Error(), "inputs.enabled: expected bool") || !strings.Contains(err.Error(), "inputs.extra is not declared") {
		t.Fatalf("Decide() error = %v", err)
	}
}

func TestModelCommanderInspectsAcceptedTaskBeforeRouting(t *testing.T) {
	t.Parallel()
	session := &fakeCommanderSession{response: &llm.ChatResponse{ID: "inspect-response", ContentBlocks: []llm.ContentBlock{{
		Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "inspect-tool", Name: "inspect_task", Input: json.RawMessage(`{"task_name":"classify"}`)},
	}}}}
	commander, err := NewModelCommander(session, nil)
	if err != nil {
		t.Fatalf("NewModelCommander() error = %v", err)
	}
	decision, err := commander.Decide(context.Background(), Snapshot{
		RunID: "run-route", Revision: 8,
		PendingRoutes: map[string][]RouteDefinition{"classify": {{Target: "support", Condition: "support"}}},
	}, nil)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Inspect.TaskName != "classify" || decision.Inspect.Revision != 8 || decision.Inspect.Limit != maxCommanderInspectionBytes || decision.Inspect.Offset != 0 {
		t.Fatalf("inspection decision = %#v", decision.Inspect)
	}
	commander.InspectionAccepted(TaskInspection{RequestID: "inspect-tool", TaskName: "classify", Summary: "support evidence"})
	if len(session.results) != 1 || session.results[0].ToolUseID != "inspect-tool" || !strings.Contains(session.results[0].Content, "support evidence") {
		t.Fatalf("inspection result = %#v", session.results)
	}
}

func TestModelCommanderRequestsFocusedTaskClarification(t *testing.T) {
	t.Parallel()
	session := &fakeCommanderSession{response: &llm.ChatResponse{ID: "clarify-response", ContentBlocks: []llm.ContentBlock{{
		Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "clarify-tool", Name: "clarify_task_result", Input: json.RawMessage(`{"task_name":"classify","question":"Which observed status code is decisive?"}`)},
	}}}}
	commander, err := NewModelCommander(session, nil)
	if err != nil {
		t.Fatalf("NewModelCommander() error = %v", err)
	}
	decision, err := commander.Decide(context.Background(), Snapshot{
		RunID: "run-route", Revision: 9,
		PendingRoutes: map[string][]RouteDefinition{"classify": {{Target: "support", Condition: "support"}}},
	}, nil)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Clarify.TaskName != "classify" || decision.Clarify.RequestID != "clarify-tool" || decision.Clarify.MessageID != "clarify-response" || decision.Clarify.Revision != 9 {
		t.Fatalf("clarification decision = %#v", decision.Clarify)
	}
	commander.ClarificationAccepted(TaskClarification{RequestID: "clarify-tool", TaskName: "classify", Answer: []store.MessagePart{{Type: "text", Text: "401"}}})
	if len(session.results) != 1 || session.results[0].ToolUseID != "clarify-tool" {
		t.Fatalf("clarification results = %#v", session.results)
	}
}

type fakeCommanderSession struct {
	tools     []llm.ToolDefinition
	loaded    []llm.Message
	results   []llm.ToolResultBlock
	response  *llm.ChatResponse
	responses []*llm.ChatResponse
	prompts   []string
	loadCount int
}

func (s *fakeCommanderSession) SetTools(tools []llm.ToolDefinition) { s.tools = tools }
func (s *fakeCommanderSession) LoadMessages(messages []llm.Message) {
	s.loaded = messages
	s.loadCount++
}
func (s *fakeCommanderSession) AddToolResults(results []llm.ToolResultBlock) {
	s.results = append(s.results, results...)
}
func (s *fakeCommanderSession) SendMessageStream(_ context.Context, prompt llm.Message, _ func(llm.StreamChunk)) (*llm.ChatResponse, error) {
	s.prompts = append(s.prompts, prompt.GetTextContent())
	if len(s.responses) > 0 {
		response := s.responses[0]
		s.responses = s.responses[1:]
		return response, nil
	}
	return s.response, nil
}
