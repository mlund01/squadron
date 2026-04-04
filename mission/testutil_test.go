package mission

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/hashicorp/hcl/v2"
	"github.com/mlund01/squadron-wire/protocol"
	"github.com/zclconf/go-cty/cty"

	"squadron/config"
	"squadron/llm"
	"squadron/streamers"
)

// ---------------------------------------------------------------------------
// Mock LLM Provider
// ---------------------------------------------------------------------------

// mockCall records a single call to the mock provider.
type mockCall struct {
	Model    string
	Messages []llm.Message
}

// mockResponse is a scripted LLM response.
type mockResponse struct {
	Content string
	// Match optionally filters which request this response should be used for.
	// When nil the response matches any request.
	Match func(*llm.ChatRequest) bool
}

// mockProvider implements llm.Provider with scripted, queue-based responses.
// Responses can be matched by predicate or consumed in FIFO order.
type mockProvider struct {
	mu        sync.Mutex
	responses []mockResponse
	calls     []mockCall
	fallback  string // returned when queue is empty (avoids panic in long exchanges)
}

func newMockProvider(responses ...mockResponse) *mockProvider {
	return &mockProvider{
		responses: responses,
		fallback:  "<ACTION>task_complete</ACTION>\n<ACTION_INPUT>{}</ACTION_INPUT>",
	}
}

// addResponses appends additional scripted responses to the queue.
func (p *mockProvider) addResponses(responses ...mockResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append(p.responses, responses...)
}

func (p *mockProvider) pickResponse(req *llm.ChatRequest) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls = append(p.calls, mockCall{Model: req.Model, Messages: req.Messages})

	// Try matched responses first
	for i, r := range p.responses {
		if r.Match != nil && r.Match(req) {
			p.responses = append(p.responses[:i], p.responses[i+1:]...)
			return r.Content
		}
	}
	// Fall back to first unmatched response
	for i, r := range p.responses {
		if r.Match == nil {
			p.responses = append(p.responses[:i], p.responses[i+1:]...)
			return r.Content
		}
	}
	return p.fallback
}

func (p *mockProvider) Chat(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	content := p.pickResponse(req)
	return &llm.ChatResponse{
		ID:           "mock-resp",
		Content:      content,
		FinishReason: "end_turn",
		Usage:        llm.Usage{InputTokens: 100, OutputTokens: 50},
	}, nil
}

func (p *mockProvider) ChatStream(_ context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	content := p.pickResponse(req)
	ch := make(chan llm.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- llm.StreamChunk{Content: content, Done: false}
		ch <- llm.StreamChunk{
			Content: "",
			Done:    true,
			Usage:   &llm.Usage{InputTokens: 100, OutputTokens: 50},
		}
	}()
	return ch, nil
}

func (p *mockProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *mockProvider) getCalls() []mockCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]mockCall, len(p.calls))
	copy(cp, p.calls)
	return cp
}

// ---------------------------------------------------------------------------
// Matchers — helpers for mockResponse.Match predicates
// ---------------------------------------------------------------------------

// matchSystemContains returns a matcher that checks if any system prompt contains substr.
func matchSystemContains(substr string) func(*llm.ChatRequest) bool {
	return func(req *llm.ChatRequest) bool {
		for _, m := range req.Messages {
			if m.Role == llm.RoleSystem && strings.Contains(m.Content, substr) {
				return true
			}
		}
		return false
	}
}

// matchLastUserContains matches when the last user message contains substr.
func matchLastUserContains(substr string) func(*llm.ChatRequest) bool {
	return func(req *llm.ChatRequest) bool {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == llm.RoleUser {
				return strings.Contains(req.Messages[i].Content, substr)
			}
		}
		return false
	}
}

// ---------------------------------------------------------------------------
// Mock Mission Streamer
// ---------------------------------------------------------------------------

type streamEvent struct {
	Type string
	Data map[string]string
}

type mockMissionStreamer struct {
	mu     sync.Mutex
	events []streamEvent
}

func newMockMissionStreamer() *mockMissionStreamer {
	return &mockMissionStreamer{}
}

func (s *mockMissionStreamer) record(eventType string, data map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, streamEvent{Type: eventType, Data: data})
}

func (s *mockMissionStreamer) getEvents() []streamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]streamEvent, len(s.events))
	copy(cp, s.events)
	return cp
}

func (s *mockMissionStreamer) hasEvent(eventType string) bool {
	for _, e := range s.getEvents() {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

func (s *mockMissionStreamer) eventCount(eventType string) int {
	count := 0
	for _, e := range s.getEvents() {
		if e.Type == eventType {
			count++
		}
	}
	return count
}

// MissionHandler implementation
func (s *mockMissionStreamer) MissionStarted(name, missionID string, taskCount int) {
	s.record("mission_started", map[string]string{"name": name, "id": missionID})
}
func (s *mockMissionStreamer) MissionCompleted(name string) {
	s.record("mission_completed", map[string]string{"name": name})
}
func (s *mockMissionStreamer) TaskStarted(taskName, objective string) {
	s.record("task_started", map[string]string{"task": taskName, "objective": objective})
}
func (s *mockMissionStreamer) TaskCompleted(taskName string) {
	s.record("task_completed", map[string]string{"task": taskName})
}
func (s *mockMissionStreamer) TaskFailed(taskName string, err error) {
	s.record("task_failed", map[string]string{"task": taskName, "error": err.Error()})
}
func (s *mockMissionStreamer) TaskIterationStarted(taskName string, totalItems int, parallel bool) {
	s.record("task_iteration_started", map[string]string{"task": taskName, "total": fmt.Sprintf("%d", totalItems)})
}
func (s *mockMissionStreamer) TaskIterationCompleted(taskName string, completedCount int) {
	s.record("task_iteration_completed", map[string]string{"task": taskName, "completed": fmt.Sprintf("%d", completedCount)})
}
func (s *mockMissionStreamer) IterationStarted(taskName string, index int, objective string) {
	s.record("iteration_started", map[string]string{"task": taskName, "index": fmt.Sprintf("%d", index)})
}
func (s *mockMissionStreamer) IterationCompleted(taskName string, index int) {
	s.record("iteration_completed", map[string]string{"task": taskName, "index": fmt.Sprintf("%d", index)})
}
func (s *mockMissionStreamer) IterationFailed(taskName string, index int, err error) {
	s.record("iteration_failed", map[string]string{"task": taskName, "index": fmt.Sprintf("%d", index)})
}
func (s *mockMissionStreamer) IterationRetrying(taskName string, index int, attempt, maxRetries int, err error) {
	s.record("iteration_retrying", map[string]string{"task": taskName, "index": fmt.Sprintf("%d", index)})
}
func (s *mockMissionStreamer) IterationReasoning(taskName string, index int, content string) {}
func (s *mockMissionStreamer) IterationAnswer(taskName string, index int, content string)    {}
func (s *mockMissionStreamer) CommanderReasoningStarted(taskName string)                      {}
func (s *mockMissionStreamer) CommanderReasoningCompleted(taskName, content string)            {}
func (s *mockMissionStreamer) CommanderAnswer(taskName, content string)                        {}
func (s *mockMissionStreamer) CommanderCallingTool(taskName, toolCallId, toolName, input string) {
	s.record("commander_tool", map[string]string{"task": taskName, "tool": toolName})
}
func (s *mockMissionStreamer) CommanderToolComplete(taskName, toolCallId, toolName, result string) {}
func (s *mockMissionStreamer) Compaction(taskName, entity string, inputTokens, tokenLimit, messagesCompacted, turnRetention int) {
}
func (s *mockMissionStreamer) SessionTurn(data protocol.SessionTurnData) {}
func (s *mockMissionStreamer) AgentStarted(taskName, agentName, instruction string) {
	s.record("agent_started", map[string]string{"task": taskName, "agent": agentName})
}
func (s *mockMissionStreamer) AgentHandler(taskName, agentName string) streamers.ChatHandler {
	return &mockChatHandler{}
}
func (s *mockMissionStreamer) AgentCompleted(taskName, agentName string) {
	s.record("agent_completed", map[string]string{"task": taskName, "agent": agentName})
}
func (s *mockMissionStreamer) RouteChosen(routerTask, targetTask, condition string, isMission bool) {
	s.record("route_chosen", map[string]string{
		"router": routerTask, "target": targetTask, "condition": condition,
	})
}

// ---------------------------------------------------------------------------
// Mock Chat Handler (for agent events)
// ---------------------------------------------------------------------------

type mockChatHandler struct{}

func (h *mockChatHandler) Welcome(agentName, modelName string)                         {}
func (h *mockChatHandler) AwaitClientAnswer() (string, error)                          { return "", nil }
func (h *mockChatHandler) Goodbye()                                                    {}
func (h *mockChatHandler) Error(err error)                                             {}
func (h *mockChatHandler) Thinking()                                                   {}
func (h *mockChatHandler) CallingTool(toolCallId, toolName, payload string)             {}
func (h *mockChatHandler) ToolComplete(toolCallId, toolName, result string)             {}
func (h *mockChatHandler) ReasoningStarted()                                           {}
func (h *mockChatHandler) PublishReasoningChunk(chunk string)                           {}
func (h *mockChatHandler) ReasoningCompleted()                                         {}
func (h *mockChatHandler) PublishAnswerChunk(chunk string)                              {}
func (h *mockChatHandler) FinishAnswer()                                               {}
func (h *mockChatHandler) AskCommander(content string)                                 {}
func (h *mockChatHandler) CommanderResponse(content string)                            {}

// ---------------------------------------------------------------------------
// Config Builders
// ---------------------------------------------------------------------------

// buildTestConfig creates a minimal config for testing with the given mission definition.
// All agents use the injected mock provider (via Provider field).
func buildTestConfig(mission config.Mission, agents ...config.Agent) *config.Config {
	promptCaching := false
	return &config.Config{
		Models: []config.Model{
			{
				Name:          "test",
				Provider:      config.ProviderAnthropic,
				AllowedModels: []string{"claude_sonnet_4"},
				APIKey:        "test-key",
				PromptCaching: &promptCaching,
			},
		},
		Agents:   agents,
		Missions: []config.Mission{mission},
		Storage:  &config.StorageConfig{Backend: "sqlite", Path: ":memory:"},
	}
}

// testAgent creates a minimal agent config.
// Model is the key from SupportedModels (e.g. "claude_sonnet_4"), not the HCL reference.
func testAgent(name string) config.Agent {
	return config.Agent{
		Name:        name,
		Model:       "claude_sonnet_4",
		Personality: "Test agent",
		Role:        "Test worker for " + name,
	}
}

// staticExpr creates an HCL expression that evaluates to the given string literal.
func staticExpr(s string) hcl.Expression {
	return hcl.StaticExpr(cty.StringVal(s), hcl.Range{})
}

// testTask creates a minimal task with a static objective.
func testTask(name, objective string) config.Task {
	return config.Task{
		Name:          name,
		ObjectiveExpr: staticExpr(objective),
		RawObjective:  objective,
	}
}

// testMission creates a minimal mission with the given tasks.
func testMission(name string, tasks []config.Task) config.Mission {
	return config.Mission{
		Name:      name,
		Commander: &config.MissionCommander{Model: "claude_sonnet_4"},
		Agents:    []string{"worker"},
		Tasks:     tasks,
	}
}

// ---------------------------------------------------------------------------
// Response Builders — helpers to construct ReAct-formatted LLM responses
// ---------------------------------------------------------------------------

func cmdCallAgent(agentName, instruction string) string {
	input, _ := json.Marshal(map[string]string{"name": agentName, "task": instruction})
	return fmt.Sprintf("<ACTION>call_agent</ACTION>\n<ACTION_INPUT>%s</ACTION_INPUT>", string(input))
}

func cmdTaskComplete() string {
	return "<ACTION>task_complete</ACTION>\n<ACTION_INPUT>{}</ACTION_INPUT>"
}

func cmdTaskCompleteFail(reason string) string {
	input, _ := json.Marshal(map[string]interface{}{"succeed": false, "reason": reason})
	return fmt.Sprintf("<ACTION>task_complete</ACTION>\n<ACTION_INPUT>%s</ACTION_INPUT>", string(input))
}

func cmdTaskCompleteRoute(route string) string {
	input, _ := json.Marshal(map[string]string{"route": route})
	return fmt.Sprintf("<ACTION>task_complete</ACTION>\n<ACTION_INPUT>%s</ACTION_INPUT>", string(input))
}

func cmdSubmitOutput(output map[string]interface{}) string {
	input, _ := json.Marshal(map[string]interface{}{"output": output})
	return fmt.Sprintf("<ACTION>submit_output</ACTION>\n<ACTION_INPUT>%s</ACTION_INPUT>", string(input))
}

func cmdSetDataset(datasetName string, items []map[string]interface{}) string {
	input, _ := json.Marshal(map[string]interface{}{
		"name":  datasetName,
		"items": items,
	})
	return fmt.Sprintf("<ACTION>set_dataset</ACTION>\n<ACTION_INPUT>%s</ACTION_INPUT>", string(input))
}

func cmdDatasetNext() string {
	return "<ACTION>dataset_next</ACTION>\n<ACTION_INPUT>{}</ACTION_INPUT>"
}

func agentAnswer(answer string) string {
	return fmt.Sprintf("<ANSWER>%s</ANSWER>", answer)
}
