package mission

import (
	"context"
	"encoding/json"
	"path/filepath"
	"squadron/aitools"
	"squadron/llm"
	"testing"

	"github.com/google/uuid"
	"squadron/config"
)

func TestMissionThreadGeneratedPersistedAndRestored(t *testing.T) {
	cfg := threadTestConfig("first")
	cfg.Storage = &config.StorageConfig{Backend: "sqlite", Path: filepath.Join(t.TempDir(), "missions.db")}
	firstProvider := newMockProvider(mockToolCall("start_task", json.RawMessage(`{"task_name":"work","model":"fast"}`)), mockResponse{Content: "Done"})
	first, err := NewRunner(cfg, "", "first", nil, WithProviderFactory(func() llm.Provider { return firstProvider }))
	if err != nil {
		t.Fatal(err)
	}
	threadID := first.ThreadID()
	if _, err := uuid.Parse(threadID); err != nil {
		t.Fatalf("generated thread: %q", threadID)
	}
	if err := first.Run(context.Background(), newMockMissionStreamer()); err != nil {
		t.Fatal(err)
	}
	runID := first.missionID
	record, err := first.stores.Missions.GetMission(runID)
	if err != nil || record.ThreadID != threadID {
		t.Fatalf("stored record: %+v, %v", record, err)
	}
	first.CloseStores()
	resumed, err := NewRunner(cfg, "", "first", nil, WithResume(runID))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ThreadID() != threadID {
		t.Fatal("resume changed thread")
	}
	resumed.CloseStores()
	if bad, err := NewRunner(cfg, "", "first", nil, WithResume(runID), WithThreadID("different")); err == nil {
		bad.CloseStores()
		t.Fatal("resume allowed changing the thread")
	}
	followupProvider := newMockProvider(mockToolCall("start_task", json.RawMessage(`{"task_name":"work","model":"fast"}`)), mockResponse{Content: "Done again"})
	followup, err := NewRunner(cfg, "", "first", nil, WithThreadID(threadID), WithProviderFactory(func() llm.Provider { return followupProvider }))
	if err != nil {
		t.Fatal(err)
	}
	defer followup.CloseStores()
	if err := followup.Run(context.Background(), newMockMissionStreamer()); err != nil {
		t.Fatal(err)
	}
	if followup.ThreadID() != threadID || followup.missionID == runID {
		t.Fatal("follow-up must share thread but have its own run ID")
	}
	fresh, err := NewRunner(cfg, "", "first", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.CloseStores()
	if fresh.ThreadID() == threadID {
		t.Fatal("independent start reused thread")
	}
}

type threadRecordingProvider struct {
	*mockProvider
	threadID string
}

func (p *threadRecordingProvider) ChatStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	p.threadID = aitools.MissionThreadFromContext(ctx)
	return p.mockProvider.ChatStream(ctx, req)
}

func TestCommanderReceivesMissionThread(t *testing.T) {
	cfg := threadTestConfig("triage")
	provider := &threadRecordingProvider{mockProvider: newMockProvider(
		mockToolCall("start_task", json.RawMessage(`{"task_name":"work","model":"fast"}`)),
		mockResponse{Content: "Done"},
	)}
	runner, err := NewRunner(cfg, "", "triage", nil, WithThreadID("shared-chain"), WithProviderFactory(func() llm.Provider { return provider }))
	if err != nil {
		t.Fatal(err)
	}
	defer runner.CloseStores()
	if err := runner.Run(context.Background(), newMockMissionStreamer()); err != nil {
		t.Fatal(err)
	}
	if provider.threadID != "shared-chain" {
		t.Fatalf("commander lost mission thread: %q", provider.threadID)
	}
}

func threadTestConfig(name string) *config.Config {
	return &config.Config{
		Storage: &config.StorageConfig{Backend: "sqlite", Path: ":memory:"},
		Models:  []config.Model{{Name: "test", Provider: config.ProviderOpenAICompatible, BaseURL: "http://localhost", Aliases: map[string]string{"fast": "fast"}}},
		Missions: []config.Mission{{
			Name: name, Directive: "Complete the declared work",
			Commander: &config.MissionCommander{Model: "fast"}, Models: []string{"fast"},
			Tasks: []config.Task{{Name: "work", RawObjective: "Do the work"}},
		}},
	}
}
