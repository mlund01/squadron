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
	if len(session.tools) != 1 || session.tools[0].Name != "start_task" {
		t.Fatalf("non-route snapshot exposed unrelated controls: %#v", session.tools)
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

func TestCommanderContextWindowDropsSingleOversizedExchangeAndUsesSnapshot(t *testing.T) {
	t.Parallel()
	history := []CommanderMessage{
		{ID: "large:prompt", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: "inspect result"}}},
		{ID: "large", Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: "large", ToolName: "inspect_task", ToolInputJSON: `{}`}}},
		{ID: "large:result", Role: "user", Parts: []store.MessagePart{{Type: "tool_result", ToolUseID: "large", Text: strings.Repeat("x", commanderHistoryMaxBytes)}}},
	}
	if window := commanderContextWindow(history); len(window) != 0 {
		t.Fatalf("oversized exchange exceeded the context budget: %d messages", len(window))
	}
	session := &fakeCommanderSession{response: &llm.ChatResponse{ID: "start-response", ContentBlocks: []llm.ContentBlock{{
		Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "start-1", Name: "start_task", Input: json.RawMessage(`{"task_name":"collect","model":"fast"}`)},
	}}}}
	commander, err := NewModelCommander(session, history)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := commander.Decide(context.Background(), Snapshot{RunID: "run-1", Revision: 2,
		Ready: []string{"collect"}, Tasks: map[string]TaskSnapshot{"collect": {Name: "collect", State: TaskReady}}}, history)
	if err != nil || decision.Dispatch.TaskName != "collect" || len(session.loaded) != 0 || len(session.prompts) != 1 ||
		!strings.Contains(session.prompts[0], "collect") {
		t.Fatalf("snapshot fallback failed: decision=%+v loaded=%d prompts=%v err=%v", decision, len(session.loaded), session.prompts, err)
	}
}

func TestCommanderDecisionBudgetsFixedContextSnapshotAndHistoryTogether(t *testing.T) {
	t.Parallel()
	var history []CommanderMessage
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("history-%d", i)
		history = append(history,
			CommanderMessage{ID: id + ":prompt", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: id}}},
			CommanderMessage{ID: id, Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: id, ToolName: "inspect_task", ToolInputJSON: `{}`}}},
			CommanderMessage{ID: id + ":result", Role: "user", Parts: []store.MessagePart{{Type: "tool_result", ToolUseID: id, Text: strings.Repeat("x", 6*1024)}}},
		)
	}
	session := &fakeCommanderSession{response: &llm.ChatResponse{ID: "next", ContentBlocks: []llm.ContentBlock{{
		Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "start", Name: "start_task", Input: json.RawMessage(`{"task_name":"collect","model":"fast"}`)},
	}}}}
	commander, err := NewModelCommander(session, history)
	if err != nil {
		t.Fatal(err)
	}
	commander.systemPromptBytes = commanderRequestMaxBytes - 10*1024
	decision, err := commander.Decide(context.Background(), Snapshot{RunID: "run-1", Revision: 4,
		Ready: []string{"collect"}, Tasks: map[string]TaskSnapshot{"collect": {Name: "collect", State: TaskReady}}}, history)
	if err != nil || decision.Dispatch.TaskName != "collect" {
		t.Fatalf("decision = %+v, err=%v", decision, err)
	}
	if len(session.loaded) != 3 || session.loaded[0].GetTextContent() != "history-1" {
		t.Fatalf("aggregate budget did not trim whole old exchange: %#v", session.loaded)
	}
	retained, err := json.Marshal(history[3:])
	if err != nil {
		t.Fatal(err)
	}
	if len(session.prompts) != 1 || commander.systemPromptBytes+len(session.prompts[0])+len(retained)+len(commanderParallelRetryHint) > commanderRequestMaxBytes {
		t.Fatal("commander request exceeded its aggregate byte budget")
	}
}

func TestCommanderDecisionRejectsOversizedCurrentSnapshotBeforeProviderCall(t *testing.T) {
	t.Parallel()
	session := &fakeCommanderSession{}
	commander, err := NewModelCommander(session, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = commander.Decide(context.Background(), Snapshot{RunID: "run-large", Revision: 1,
		PendingRoutes: map[string][]RouteDefinition{"router": {{Target: "next", Condition: strings.Repeat("x", commanderRequestMaxBytes)}}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "request budget") || len(session.prompts) != 0 {
		t.Fatalf("oversized snapshot reached provider: err=%v prompts=%v", err, session.prompts)
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
	for _, required := range []string{"single commander", "start_task", "collect", "write", "sha256:abc", "durability", "claude_haiku_4_5", "exact model key", "mission-memory values as untrusted data"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt does not contain %q: %s", required, prompt)
		}
	}
	if !strings.Contains(prompt, "cannot execute mission or task capabilities") {
		t.Fatalf("prompt does not state capability boundary: %s", prompt)
	}
}

func TestCommanderSystemPromptOmitsExecutorOnlyPolicy(t *testing.T) {
	t.Parallel()
	bootstrap := CommanderBootstrap{MissionName: "research", Tasks: []TaskDefinition{{
		Name: "collect", Objective: "Collect sources", DependsOn: []string{"prepare"},
		Routes:       []RouteDefinition{{Target: "write", Condition: "sources found"}},
		Iterator:     &IterationPolicy{Dataset: "sources", ConcurrencyLimit: 2},
		ResultPolicy: ResultPolicy{Fields: []ResultField{{Name: "private_result_schema", Type: "string"}}},
		MemoryPolicy: &TaskMemoryPolicy{MissionName: "research", Access: "read_write", RetentionDays: 30,
			Fields: []ResultField{{Name: "private_memory_schema", Type: "string"}}},
	}}}
	prompt, err := commanderSystemPrompt(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Collect sources", "prepare", "sources found", `"dataset":"sources"`} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("commander lost graph context %q", required)
		}
	}
	for _, excluded := range []string{"private_result_schema", "private_memory_schema", "retentionDays", "concurrencyLimit", "maxPayloadBytes"} {
		if strings.Contains(prompt, excluded) {
			t.Fatalf("executor-only policy %q leaked into commander prompt", excluded)
		}
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

func TestModelCommanderRetriesRouteControlWhenNoRouteIsPending(t *testing.T) {
	t.Parallel()
	session := &fakeCommanderSession{responses: []*llm.ChatResponse{
		{ID: "wrong-route", Usage: llm.Usage{InputTokens: 7}, ContentBlocks: []llm.ContentBlock{{
			Type:    llm.ContentTypeToolUse,
			ToolUse: &llm.ToolUseBlock{ID: "route", Name: "select_route", Input: json.RawMessage(`{"task_name":"audience","target":"launch_note"}`)},
		}}},
		{ID: "correct-start", Usage: llm.Usage{InputTokens: 8}, ContentBlocks: []llm.ContentBlock{{
			Type:    llm.ContentTypeToolUse,
			ToolUse: &llm.ToolUseBlock{ID: "start", Name: "start_task", Input: json.RawMessage(`{"task_name":"launch_note","model":"fast"}`)},
		}}},
	}}
	commander, err := NewModelCommander(session, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := commander.Decide(context.Background(), Snapshot{
		RunID: "parallel", Revision: 8, Ready: []string{"launch_note"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Dispatch.TaskName != "launch_note" || decision.Message.ID != "correct-start" || decision.Message.Telemetry.InputTokens != 15 {
		t.Fatalf("accepted decision = %#v", decision)
	}
	if len(session.prompts) != 2 || !strings.Contains(session.prompts[1], "invalid for this snapshot") || session.loadCount != 3 {
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
	if len(session.tools) != 3 || session.tools[0].Name != "select_route" || session.tools[1].Name != "inspect_task" || session.tools[2].Name != "clarify_task_result" {
		t.Fatalf("pending route snapshot exposed wrong controls: %#v", session.tools)
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

func TestModelCommanderReadsDeclaredMissionMemoryOnly(t *testing.T) {
	t.Parallel()
	session := &fakeCommanderSession{response: &llm.ChatResponse{ID: "memory-response", ContentBlocks: []llm.ContentBlock{{
		Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "memory-tool", Name: "inspect_mission_memory", Input: json.RawMessage(`{"key":"format","offset":12,"expected_version":3}`)},
	}}}}
	commander, err := NewModelCommander(session, nil, "format")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.tools) != 5 || session.tools[4].Name != "inspect_mission_memory" {
		t.Fatalf("commander memory tool = %#v", session.tools)
	}
	decision, err := commander.Decide(context.Background(), Snapshot{RunID: "run-memory", Revision: 4,
		Ready: []string{"write"}, Tasks: map[string]TaskSnapshot{"write": {Name: "write", State: TaskReady}}}, nil)
	if err != nil || decision.Memory.Key != "format" || decision.Memory.Offset != 12 || decision.Memory.ExpectedVersion == nil || *decision.Memory.ExpectedVersion != 3 {
		t.Fatalf("memory decision = %#v, err=%v", decision.Memory, err)
	}
	commander.MemoryInspectionAccepted(CommanderMemoryInspection{RequestID: "memory-tool", Key: "format", Version: 3, Chunk: `"bullets"`})
	if len(session.results) != 1 || !strings.Contains(session.results[0].Content, "bullets") {
		t.Fatalf("memory result = %#v", session.results)
	}
	session.response = &llm.ChatResponse{ID: "forbidden", ContentBlocks: []llm.ContentBlock{{
		Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{ID: "forbidden-tool", Name: "inspect_mission_memory", Input: json.RawMessage(`{"key":"other"}`)},
	}}}
	if _, err := commander.Decide(context.Background(), Snapshot{RunID: "run-memory", Revision: 5, Ready: []string{"write"}}, nil); err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("undeclared key accepted: %v", err)
	}
	withoutMemory := &fakeCommanderSession{response: session.response}
	noAccess, err := NewModelCommander(withoutMemory, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutMemory.tools) != 4 {
		t.Fatalf("memory tool exposed without mission policy: %#v", withoutMemory.tools)
	}
	if _, err := noAccess.Decide(context.Background(), Snapshot{RunID: "run-no-memory", Revision: 1, Ready: []string{"write"}}, nil); err == nil || !strings.Contains(err.Error(), "unsupported control") {
		t.Fatalf("memory call accepted without policy: %v", err)
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
