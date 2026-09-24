package runtime

import (
	"context"
	"encoding/json"
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
		ConfigurationVersion: "sha256:abc",
	})
	if err != nil {
		t.Fatalf("commanderSystemPrompt() error = %v", err)
	}
	for _, required := range []string{"single commander", "start_task", "collect", "write", "sha256:abc", "durability"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt does not contain %q: %s", required, prompt)
		}
	}
	if !strings.Contains(prompt, "cannot execute mission or task capabilities") {
		t.Fatalf("prompt does not state capability boundary: %s", prompt)
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
	if decision.Inspect.TaskName != "classify" || decision.Inspect.Revision != 8 {
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
	tools    []llm.ToolDefinition
	loaded   []llm.Message
	results  []llm.ToolResultBlock
	response *llm.ChatResponse
}

func (s *fakeCommanderSession) SetTools(tools []llm.ToolDefinition) { s.tools = tools }
func (s *fakeCommanderSession) LoadMessages(messages []llm.Message) { s.loaded = messages }
func (s *fakeCommanderSession) AddToolResults(results []llm.ToolResultBlock) {
	s.results = append(s.results, results...)
}
func (s *fakeCommanderSession) SendMessageStream(context.Context, llm.Message, func(llm.StreamChunk)) (*llm.ChatResponse, error) {
	return s.response, nil
}
