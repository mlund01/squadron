package wsbridge_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mlund01/squadron-sdk/protocol"

	"squadron/config"
	"squadron/store"
	"squadron/wsbridge"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// mockCommander is a minimal WebSocket server that mimics a commander for testing.
type mockCommander struct {
	srv  *httptest.Server
	conn *websocket.Conn
	t    *testing.T
}

func newMockCommander(t *testing.T) *mockCommander {
	t.Helper()
	mc := &mockCommander{t: t}

	connCh := make(chan *websocket.Conn, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		connCh <- ws
	})
	mc.srv = httptest.NewServer(mux)

	// Wait for connection from client (will be set after client.Connect())
	go func() {
		mc.conn = <-connCh
	}()

	t.Cleanup(func() {
		if mc.conn != nil {
			mc.conn.Close()
		}
		mc.srv.Close()
	})

	return mc
}

func (mc *mockCommander) wsURL() string {
	return "ws" + strings.TrimPrefix(mc.srv.URL, "http") + "/ws"
}

func (mc *mockCommander) waitForConnection() {
	for i := 0; i < 50; i++ {
		if mc.conn != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	mc.t.Fatal("timed out waiting for WS connection")
}

func (mc *mockCommander) readEnvelope() *protocol.Envelope {
	mc.t.Helper()
	mc.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := mc.conn.ReadMessage()
	if err != nil {
		mc.t.Fatalf("read from client: %v", err)
	}
	var env protocol.Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		mc.t.Fatalf("unmarshal: %v", err)
	}
	return &env
}

func (mc *mockCommander) sendEnvelope(env *protocol.Envelope) {
	mc.t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		mc.t.Fatalf("marshal: %v", err)
	}
	if err := mc.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		mc.t.Fatalf("write: %v", err)
	}
}

func testConfig(wsURL string) *config.Config {
	return &config.Config{
		Commander: &config.CommanderConfig{
			URL:                wsURL,
			InstanceName:       "test-instance",
			ReconnectInterval:  1,
		},
		Models: []config.Model{
			{Name: "my-model", Provider: "openai", AllowedModels: []string{"gpt-4"}},
		},
		Agents: []config.Agent{
			{Name: "my-agent", Model: "my-model", Tools: []string{"web_search"}},
		},
		Missions: []config.Mission{
			{Name: "my-mission", Agents: []string{"my-agent"}},
		},
		Variables: []config.Variable{
			{Name: "api_key", Secret: true},
		},
	}
}

func TestClientConnectAndRegister(t *testing.T) {
	mc := newMockCommander(t)
	cfg := testConfig(mc.wsURL())
	stores := store.NewMemoryBundle()
	defer stores.Close()

	client := wsbridge.NewClient(cfg, ".", stores, "1.0.0")

	// Handle registration in background (mock commander side)
	go func() {
		mc.waitForConnection()

		// Read the register request
		env := mc.readEnvelope()
		if env.Type != protocol.TypeRegister {
			t.Errorf("expected register, got %s", env.Type)
			return
		}

		var payload protocol.RegisterPayload
		if err := protocol.DecodePayload(env, &payload); err != nil {
			t.Errorf("decode: %v", err)
			return
		}

		if payload.InstanceName != "test-instance" {
			t.Errorf("expected instance name 'test-instance', got %q", payload.InstanceName)
		}
		if payload.Version != "1.0.0" {
			t.Errorf("expected version '1.0.0', got %q", payload.Version)
		}
		if len(payload.Config.Models) != 1 {
			t.Errorf("expected 1 model, got %d", len(payload.Config.Models))
		}
		if len(payload.Config.Agents) != 1 {
			t.Errorf("expected 1 agent, got %d", len(payload.Config.Agents))
		}
		if len(payload.Config.Variables) != 1 {
			t.Errorf("expected 1 variable, got %d", len(payload.Config.Variables))
		}
		if !payload.Config.Variables[0].Secret {
			t.Error("expected api_key to be marked as secret")
		}

		// Send ack
		ack, _ := protocol.NewResponse(env.RequestID, protocol.TypeRegisterAck, &protocol.RegisterAckPayload{
			InstanceID: "inst-42",
			Accepted:   true,
		})
		mc.sendEnvelope(ack)
	}()

	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	if client.InstanceID() != "inst-42" {
		t.Errorf("expected instance ID 'inst-42', got %q", client.InstanceID())
	}
}

func TestClientHandlesGetConfig(t *testing.T) {
	mc := newMockCommander(t)
	cfg := testConfig(mc.wsURL())
	stores := store.NewMemoryBundle()
	defer stores.Close()

	client := wsbridge.NewClient(cfg, ".", stores, "1.0.0")

	// Handle registration
	go func() {
		mc.waitForConnection()
		env := mc.readEnvelope()
		ack, _ := protocol.NewResponse(env.RequestID, protocol.TypeRegisterAck, &protocol.RegisterAckPayload{
			InstanceID: "inst-1",
			Accepted:   true,
		})
		mc.sendEnvelope(ack)
	}()

	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	// Commander sends get_config request to the instance
	getConfigReq, _ := protocol.NewRequest(protocol.TypeGetConfig, &protocol.GetConfigPayload{})
	mc.sendEnvelope(getConfigReq)

	// Read the response
	resp := mc.readEnvelope()
	if resp.Type != protocol.TypeGetConfigResult {
		t.Fatalf("expected get_config_result, got %s", resp.Type)
	}
	if resp.RequestID != getConfigReq.RequestID {
		t.Errorf("expected request ID %q, got %q", getConfigReq.RequestID, resp.RequestID)
	}

	var result protocol.GetConfigResultPayload
	if err := protocol.DecodePayload(resp, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(result.Config.Models) != 1 {
		t.Errorf("expected 1 model, got %d", len(result.Config.Models))
	}
	if result.Config.Models[0].Name != "my-model" {
		t.Errorf("expected model name 'my-model', got %q", result.Config.Models[0].Name)
	}
	if result.Config.Models[0].Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", result.Config.Models[0].Provider)
	}
}

func TestClientHandlesHeartbeat(t *testing.T) {
	mc := newMockCommander(t)
	cfg := testConfig(mc.wsURL())
	stores := store.NewMemoryBundle()
	defer stores.Close()

	client := wsbridge.NewClient(cfg, ".", stores, "1.0.0")

	go func() {
		mc.waitForConnection()
		env := mc.readEnvelope()
		ack, _ := protocol.NewResponse(env.RequestID, protocol.TypeRegisterAck, &protocol.RegisterAckPayload{
			InstanceID: "inst-1",
			Accepted:   true,
		})
		mc.sendEnvelope(ack)
	}()

	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	// Send heartbeat
	heartbeat, _ := protocol.NewRequest(protocol.TypeHeartbeat, &protocol.HeartbeatPayload{})
	mc.sendEnvelope(heartbeat)

	resp := mc.readEnvelope()
	if resp.Type != protocol.TypeHeartbeatAck {
		t.Fatalf("expected heartbeat_ack, got %s", resp.Type)
	}
}

func TestClientHandlesGetMissions(t *testing.T) {
	mc := newMockCommander(t)
	cfg := testConfig(mc.wsURL())
	stores := store.NewMemoryBundle()
	defer stores.Close()

	// Create some mission records in the store
	stores.Missions.CreateMission("search-mission", `{"query": "test"}`, `{}`)

	client := wsbridge.NewClient(cfg, ".", stores, "1.0.0")

	go func() {
		mc.waitForConnection()
		env := mc.readEnvelope()
		ack, _ := protocol.NewResponse(env.RequestID, protocol.TypeRegisterAck, &protocol.RegisterAckPayload{
			InstanceID: "inst-1",
			Accepted:   true,
		})
		mc.sendEnvelope(ack)
	}()

	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	// Commander requests mission history
	getMissions, _ := protocol.NewRequest(protocol.TypeGetMissions, &protocol.GetMissionsPayload{
		Limit: 10,
	})
	mc.sendEnvelope(getMissions)

	resp := mc.readEnvelope()
	if resp.Type != protocol.TypeGetMissionsResult {
		t.Fatalf("expected get_missions_result, got %s", resp.Type)
	}

	var result protocol.GetMissionsResultPayload
	if err := protocol.DecodePayload(resp, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
	if len(result.Missions) != 1 {
		t.Fatalf("expected 1 mission, got %d", len(result.Missions))
	}
	if result.Missions[0].Name != "search-mission" {
		t.Errorf("expected name 'search-mission', got %q", result.Missions[0].Name)
	}
}

func TestConfigConversion(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "m1", Provider: "anthropic", AllowedModels: []string{"claude-3"}},
			{Name: "m2", Provider: "openai", AllowedModels: []string{"gpt-4", "gpt-3.5"}},
		},
		Agents: []config.Agent{
			{Name: "agent1", Model: "m1", Tools: []string{"tool1", "tool2"}},
		},
		Missions: []config.Mission{
			{
				Name:   "mission1",
				Agents: []string{"agent1"},
				Inputs: []config.MissionInput{
					{Name: "query", Type: "string", Description: "Search query"},
				},
				Tasks: []config.Task{
					{Name: "task1", Agents: []string{"agent1"}, DependsOn: []string{}},
					{Name: "task2", DependsOn: []string{"task1"}},
				},
			},
		},
		Plugins: []config.Plugin{
			{Name: "playwright", Source: "github.com/example/playwright"},
		},
		Variables: []config.Variable{
			{Name: "api_key", Secret: true},
			{Name: "base_url", Secret: false},
		},
	}

	ic := wsbridge.ConfigToInstanceConfig(cfg)

	if len(ic.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(ic.Models))
	}
	if ic.Models[0].Provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", ic.Models[0].Provider)
	}
	if ic.Models[1].Model != "gpt-4" {
		t.Errorf("expected first allowed model 'gpt-4', got %q", ic.Models[1].Model)
	}

	if len(ic.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(ic.Agents))
	}
	if len(ic.Agents[0].Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(ic.Agents[0].Tools))
	}

	if len(ic.Missions) != 1 {
		t.Fatalf("expected 1 mission, got %d", len(ic.Missions))
	}
	if len(ic.Missions[0].Tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(ic.Missions[0].Tasks))
	}
	if ic.Missions[0].Tasks[0].Agent != "agent1" {
		t.Errorf("expected agent 'agent1', got %q", ic.Missions[0].Tasks[0].Agent)
	}

	if len(ic.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(ic.Plugins))
	}

	if len(ic.Variables) != 2 {
		t.Fatalf("expected 2 variables, got %d", len(ic.Variables))
	}
	if !ic.Variables[0].Secret {
		t.Error("expected api_key to be secret")
	}
	if ic.Variables[1].Secret {
		t.Error("expected base_url to not be secret")
	}
}
