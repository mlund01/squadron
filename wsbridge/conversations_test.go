package wsbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mlund01/squadron-wire/protocol"
	"squadron/config"
	"squadron/store"
)

func TestConversationAgentScope(t *testing.T) {
	cfg := &config.Config{
		Agents: []config.Agent{{Name: "reviewer", Role: "global"}},
		Missions: []config.Mission{
			{Name: "release", LocalAgents: []config.Agent{{Name: "reviewer", Role: "release"}}},
			{Name: "audit", LocalAgents: []config.Agent{{Name: "reviewer", Role: "audit"}}},
		},
	}
	for _, scope := range []string{"", "release", "audit"} {
		agent, err := resolveConversationAgent(cfg, "reviewer", scope)
		want := scope
		if want == "" {
			want = "global"
		}
		if err != nil || agent.Role != want {
			t.Fatalf("scope %q: got %v, %v", scope, agent, err)
		}
	}
	if _, err := resolveConversationAgent(cfg, "reviewer", "missing"); err == nil {
		t.Fatal("missing mission fell back to global agent")
	}
}

func TestConversationMemoryIdentityStaysOwnerScoped(t *testing.T) {
	p := conversationRequest{Owner: "user-1", AgentName: "reviewer", Purpose: "session", Mode: "interactive"}
	identity := p.identity()
	identity.MemoryKey = "opaque-session-memory-key"
	if !identity.matches(p) {
		t.Fatal("stored memory key prevented its own conversation from resuming")
	}
	p.Owner = "user-2"
	if identity.matches(p) {
		t.Fatal("conversation memory became accessible to another owner")
	}
	source := &config.Agent{Name: "reviewer", Memory: &config.AgentMemoryPolicy{Mode: "persistent", Scope: "owner", RetentionDays: 30}}
	authoring, _ := conversationAgentConfig(source, "authoring", "interactive")
	if authoring.Memory != nil {
		t.Fatal("authoring assistant inherited the target agent's memory")
	}
}

func TestAgentDefinitionIncludesScopedSource(t *testing.T) {
	source := &config.ConfigSource{Path: "missions/release.hcl", StartLine: 8, EndLine: 12, Content: `agent "reviewer" { personality = "Release" }`, FileRevision: "snapshot"}
	cfg := &config.Config{
		Agents:   []config.Agent{{Name: "reviewer", Source: &config.ConfigSource{Path: "agents.hcl"}}},
		Missions: []config.Mission{{Name: "release", LocalAgents: []config.Agent{{Name: "reviewer", Source: source}}}},
	}
	client := NewClient(cfg, true, "", "", nil, "test")
	defer client.Close()
	request, _ := protocol.NewRequest("get_agent_definition", conversationRequest{AgentName: "reviewer", Mission: "release"})
	response, err := client.handleAgentDefinition(request)
	if err != nil {
		t.Fatal(err)
	}
	var definition struct {
		Source *config.ConfigSource `json:"source"`
	}
	if err := protocol.DecodePayload(response, &definition); err != nil || !reflect.DeepEqual(definition.Source, source) {
		t.Fatalf("wrong source: %#v, %v", definition.Source, err)
	}
}

func TestMissionDefinitionIncludesSource(t *testing.T) {
	source := &config.ConfigSource{Path: "missions/release.hcl", StartLine: 3, EndLine: 9, Content: `mission "release" {}`, FileRevision: "snapshot"}
	client := NewClient(&config.Config{Missions: []config.Mission{{Name: "release", Source: source}}}, true, "", "", nil, "test")
	defer client.Close()
	request, _ := protocol.NewRequest("get_mission_definition", missionDefinitionRequest{MissionName: "release"})

	response, err := client.handleMissionDefinition(request)
	if err != nil {
		t.Fatal(err)
	}
	var definition struct {
		Name   string               `json:"name"`
		Source *config.ConfigSource `json:"source"`
	}
	if err := protocol.DecodePayload(response, &definition); err != nil || definition.Name != "release" || !reflect.DeepEqual(definition.Source, source) {
		t.Fatalf("wrong definition: %#v, %v", definition, err)
	}
}

func TestConversationModes(t *testing.T) {
	source := config.Agent{
		Name: "reviewer", Model: "models.test.small", Reasoning: "high",
		Personality: "Target personality", Role: "Operational role",
		Tools: []string{"builtins.http.post"}, Skills: []string{"skills.release"},
		LocalSkills: []config.Skill{{Name: "deploy", Instructions: "Deploy immediately"}},
	}
	author, mode := conversationAgentConfig(&source, "authoring", "interactive")
	if mode != config.ModeChat || author.Model != source.Model || author.Reasoning != source.Reasoning {
		t.Fatal("authoring did not retain model settings in chat mode")
	}
	if len(author.Tools) != 0 || len(author.Skills) != 0 || len(author.LocalSkills) != 0 || author.Role != "" {
		t.Fatal("authoring inherited operational grants or role")
	}
	if !strings.Contains(author.Personality, "Start session") || !strings.Contains(author.Personality, "cannot save") {
		t.Fatal("authoring lacks execution/apply boundary")
	}
	for _, tc := range []struct {
		mode string
		want config.AgentMode
	}{{"interactive", config.ModeChat}, {"task", config.ModeMission}} {
		got, runtimeMode := conversationAgentConfig(&source, "session", tc.mode)
		if runtimeMode != tc.want || !reflect.DeepEqual(got.Tools, source.Tools) || !reflect.DeepEqual(got.LocalSkills, source.LocalSkills) || !strings.HasPrefix(got.Personality, source.Personality) {
			t.Fatalf("%s did not preserve operational configuration/mode", tc.mode)
		}
	}
	if source.Personality != "Target personality" {
		t.Fatal("source configuration mutated")
	}
}

func TestConversationValidation(t *testing.T) {
	valid := conversationRequest{Operation: "send", Owner: "user-1", AgentName: "reviewer", Purpose: "authoring", Mode: "interactive", Content: "Review the configuration"}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*conversationRequest){
		"no owner":             func(p *conversationRequest) { p.Owner = "" },
		"no agent":             func(p *conversationRequest) { p.AgentName = "" },
		"invalid purpose":      func(p *conversationRequest) { p.Purpose = "execute" },
		"invalid mode":         func(p *conversationRequest) { p.Mode = "chat" },
		"task authoring":       func(p *conversationRequest) { p.Mode = "task" },
		"empty message":        func(p *conversationRequest) { p.Content = " \n" },
		"large message":        func(p *conversationRequest) { p.Content = strings.Repeat("a", (64<<10)+1) },
		"invalid operation":    func(p *conversationRequest) { p.Operation = "delete" },
		"read without session": func(p *conversationRequest) { p.Operation = "read" },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if request.validate() == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestConversationReadIsScopedAndDurable(t *testing.T) {
	client, request := storedConversation(t)
	response, err := client.handleAgentConversation(conversationEnvelope(t, request))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Messages []protocol.ChatMessageInfo `json:"messages"`
		Running  bool                       `json:"running"`
	}
	if err := protocol.DecodePayload(response, &state); err != nil {
		t.Fatal(err)
	}
	if state.Running || len(state.Messages) != 2 || state.Messages[0].Content != "hello" || state.Messages[1].Content != "answer" {
		t.Fatalf("unexpected restored state: %#v", state)
	}
	for name, mutate := range map[string]func(*conversationRequest){
		"user":      func(p *conversationRequest) { p.Owner = "other" },
		"agent":     func(p *conversationRequest) { p.AgentName = "other" },
		"mission":   func(p *conversationRequest) { p.Mission = "other" },
		"purpose":   func(p *conversationRequest) { p.Purpose = "authoring" },
		"mode":      func(p *conversationRequest) { p.Mode = "task" },
		"legacy ID": func(p *conversationRequest) { p.SessionID = strings.TrimPrefix(p.SessionID, "cc_") },
	} {
		t.Run(name, func(t *testing.T) {
			modified := request
			mutate(&modified)
			for _, operation := range []string{"read", "stop", "send"} {
				modified.Operation, modified.Content = operation, "hi"
				if _, err := client.handleAgentConversation(conversationEnvelope(t, modified)); err == nil {
					t.Fatalf("%s accepted wrong %s", operation, name)
				}
			}
		})
	}
	legacyID := strings.TrimPrefix(request.SessionID, "cc_")
	legacy, _ := protocol.NewRequest(protocol.TypeGetChatMessages, protocol.GetChatMessagesPayload{SessionID: legacyID})
	if _, err := client.handleGetChatMessages(legacy); err == nil {
		t.Fatal("legacy API exposed scoped messages")
	}
	sessions, total, err := client.stores.Sessions.ListChatSessions("", 50, 0)
	if err != nil || total != 0 || len(sessions) != 0 {
		t.Fatalf("legacy history exposed scoped conversation: %v, %d, %v", sessions, total, err)
	}
}

func TestConversationBusyAndStop(t *testing.T) {
	client, request := storedConversation(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := NewWSChatHandler(client, request.SessionID)
	handler.PublishAnswerChunk("partial")
	client.conversations[request.SessionID] = &agentConversation{identity: request.identity(), running: true, cancel: cancel, handler: handler}
	request.Operation, request.Content = "send", "duplicate"
	if _, err := client.handleAgentConversation(conversationEnvelope(t, request)); err == nil {
		t.Fatal("accepted concurrent turn")
	}
	request.Operation = "stop"
	response, err := client.handleAgentConversation(conversationEnvelope(t, request))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		PartialAnswer string `json:"partialAnswer"`
	}
	if err := protocol.DecodePayload(response, &state); err != nil || state.PartialAnswer != "partial" {
		t.Fatalf("streamed answer = %q, err = %v", state.PartialAnswer, err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("stop did not cancel execution")
	}
	if len(client.send) != 0 {
		t.Fatal("scoped answer leaked into legacy event stream")
	}
}

// Exercise real agent construction, streaming, persistence and restart without
// contacting a model provider or granting any operational tools.
func TestConversationRoundTrip(t *testing.T) {
	for _, tc := range []struct{ purpose, mode string }{
		{"authoring", "interactive"}, {"session", "interactive"}, {"session", "task"},
	} {
		t.Run(tc.purpose+"/"+tc.mode, func(t *testing.T) {
			requests := make(chan string, 8)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requests <- string(body)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"<ANSWER>Test response</ANSWER>\",\"output_index\":0}\n\n")
				fmt.Fprint(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"<ANSWER>Test response</ANSWER>\"}]}}\n\n")
				fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
			}))
			defer provider.Close()
			client, _ := storedConversation(t)
			cfg := &config.Config{
				Models:   []config.Model{{Name: "test", Provider: config.ProviderOllama, BaseURL: provider.URL, Aliases: map[string]string{"small": "synthetic"}}},
				Missions: []config.Mission{{Name: "release", LocalAgents: []config.Agent{{Name: "reviewer", Model: "small", Personality: "A helpful reviewer"}}}},
			}
			client.SetConfig(cfg)
			request := conversationRequest{Operation: "send", Owner: "user-1", AgentName: "reviewer", Mission: "release", Purpose: tc.purpose, Mode: tc.mode, Content: "first turn"}
			response, err := client.handleAgentConversation(conversationEnvelope(t, request))
			if err != nil {
				t.Fatal(err)
			}
			var state struct {
				SessionID string `json:"sessionId"`
			}
			if err := protocol.DecodePayload(response, &state); err != nil || state.SessionID == "" {
				t.Fatalf("missing session: %v", err)
			}
			request.SessionID = state.SessionID
			waitForConversation(t, client, request, 2)
			select {
			case body := <-requests:
				if !strings.Contains(body, "first turn") {
					t.Fatal("provider did not receive user message")
				}
			default:
				t.Fatal("provider was not called")
			}
			// Rehydrate using only durable state, as happens after runner restart.
			client.Close()
			restored := NewClient(cfg, true, "", t.TempDir(), client.stores, "test")
			t.Cleanup(restored.Close)
			request.Content = "second turn"
			if _, err := restored.handleAgentConversation(conversationEnvelope(t, request)); err != nil {
				t.Fatal(err)
			}
			waitForConversation(t, restored, request, 4)
			body := <-requests
			if !strings.Contains(body, "first turn") || !strings.Contains(body, "Test response") || !strings.Contains(body, "second turn") {
				t.Fatal("conversation history not restored for follow-up")
			}
		})
	}
}

func waitForConversation(t *testing.T, client *Client, request conversationRequest, messageCount int) {
	t.Helper()
	request.Operation = "read"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.handleAgentConversation(conversationEnvelope(t, request))
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			Running  bool                       `json:"running"`
			Messages []protocol.ChatMessageInfo `json:"messages"`
		}
		if err := protocol.DecodePayload(response, &state); err != nil {
			t.Fatal(err)
		}
		if !state.Running && len(state.Messages) == messageCount {
			if state.Messages[messageCount-1].Content != "Test response" {
				t.Fatalf("unexpected answer: %q", state.Messages[messageCount-1].Content)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("conversation did not complete")
}

func storedConversation(t *testing.T) (*Client, conversationRequest) {
	t.Helper()
	bundle, err := store.NewSQLiteBundle(filepath.Join(t.TempDir(), "conversations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bundle.Close() })
	c := NewClient(&config.Config{}, true, "", t.TempDir(), bundle, "test")
	t.Cleanup(c.Close)
	id, err := bundle.Sessions.CreateChatSession("cc_conversation:session:reviewer", "test")
	if err != nil {
		t.Fatal(err)
	}
	p := conversationRequest{Operation: "read", Owner: "user-1", AgentName: "reviewer", Mission: "release", Purpose: "session", Mode: "interactive", SessionID: "cc_" + id}
	metadata, _ := json.Marshal(p.identity())
	for _, row := range []struct{ role, content string }{{"system", string(metadata)}, {"user", "hello"}, {"assistant", "answer"}} {
		if err := bundle.Sessions.AppendMessage(id, row.role, row.content, time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return c, p
}

func conversationEnvelope(t *testing.T, p conversationRequest) *protocol.Envelope {
	t.Helper()
	env, err := protocol.NewRequest("agent_conversation", p)
	if err != nil {
		t.Fatal(err)
	}
	return env
}
