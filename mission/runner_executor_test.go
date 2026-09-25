package mission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"squadron/config"
	"squadron/llm"
	missionruntime "squadron/mission/runtime"
)

func TestMissionScopedRunnerRestartsInFlightTaskWithoutNewAttempt(t *testing.T) {
	provider := &restartTestProvider{taskStarted: make(chan struct{})}
	storagePath := filepath.Join(t.TempDir(), "runtime.db")
	cfg := &config.Config{
		Storage: &config.StorageConfig{Backend: "sqlite", Path: storagePath},
		Models:  []config.Model{{Name: "test", Provider: config.ProviderOpenAICompatible, BaseURL: "http://localhost", Aliases: map[string]string{"fast": "fast"}}},
		Missions: []config.Mission{{
			Name: "restart_policy", Directive: "Complete the declared work",
			Commander: &config.MissionCommander{Model: "fast"}, Models: []string{"fast"},
			Tasks: []config.Task{{Name: "work", RawObjective: "Do the work"}},
		}},
	}
	first, err := NewRunner(cfg, "", "restart_policy", nil, WithProviderFactory(func() llm.Provider { return provider }))
	if err != nil {
		t.Fatalf("NewRunner(first) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- first.Run(ctx, newMockMissionStreamer()) }()
	<-provider.taskStarted
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("first Run() error = %v, want context canceled", err)
	}
	runID := first.missionID
	record, err := first.stores.Missions.GetMission(runID)
	if err != nil || record.Status != "stopped" {
		t.Fatalf("interrupted mission record = %#v, %v", record, err)
	}
	first.CloseStores()

	second, err := NewRunner(cfg, "", "restart_policy", nil, WithResume(runID), WithProviderFactory(func() llm.Provider { return provider }))
	if err != nil {
		t.Fatalf("NewRunner(second) error = %v", err)
	}
	if err := second.Run(context.Background(), newMockMissionStreamer()); err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	restored, _, err := missionruntime.RestoreDurableHarness(second.stores.Runtime, runID)
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	snapshot := restored.Snapshot()
	if snapshot.RunState != missionruntime.RunCompleted || snapshot.Tasks["work"].Attempt != 1 {
		t.Fatalf("restarted snapshot = %#v", snapshot)
	}
	if calls := provider.callCount(); calls != 3 {
		t.Fatalf("provider calls = %d, want commander + interrupted worker + resumed worker", calls)
	}
	costs, err := second.stores.Costs.GetCostsByMission(runID)
	if err != nil {
		t.Fatalf("GetCostsByMission() error = %v", err)
	}
	if len(costs) != 2 {
		t.Fatalf("cost records = %#v, want one completed commander turn and one completed worker turn", costs)
	}
	if costs[0].InputTokens+costs[1].InputTokens != 18 || costs[0].OutputTokens+costs[1].OutputTokens != 7 {
		t.Fatalf("cost token totals = input %d output %d, want 18 and 7", costs[0].InputTokens+costs[1].InputTokens, costs[0].OutputTokens+costs[1].OutputTokens)
	}
	second.CloseStores()

	third, err := NewRunner(cfg, "", "restart_policy", nil, WithResume(runID), WithProviderFactory(func() llm.Provider { return provider }))
	if err != nil {
		t.Fatalf("NewRunner(third) error = %v", err)
	}
	defer third.CloseStores()
	if err := third.Run(context.Background(), newMockMissionStreamer()); err != nil {
		t.Fatalf("second replay Run() error = %v", err)
	}
	costs, err = third.stores.Costs.GetCostsByMission(runID)
	if err != nil {
		t.Fatalf("GetCostsByMission() after replay error = %v", err)
	}
	if len(costs) != 2 || provider.callCount() != 3 {
		t.Fatalf("replay duplicated work or accounting: calls=%d costs=%#v", provider.callCount(), costs)
	}
}

type restartTestProvider struct {
	mu          sync.Mutex
	calls       int
	taskStarted chan struct{}
}

func (p *restartTestProvider) next() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.calls
}

func (p *restartTestProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *restartTestProvider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	stream, err := p.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}
	response := &llm.ChatResponse{ID: "restart-test", FinishReason: "end_turn"}
	for chunk := range stream {
		response.Content += chunk.Content
		if chunk.ContentBlocks != nil {
			response.ContentBlocks = chunk.ContentBlocks
		}
		if chunk.Usage != nil {
			response.Usage = *chunk.Usage
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return response, nil
}

func (p *restartTestProvider) ChatStream(ctx context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	call := p.next()
	ch := make(chan llm.StreamChunk, 2)
	go func() {
		defer close(ch)
		switch call {
		case 1:
			response := mockToolCall("start_task", json.RawMessage(`{"task_name":"work","model":"fast"}`))
			ch <- llm.StreamChunk{Done: true, ContentBlocks: response.ContentBlocks, Usage: &llm.Usage{InputTokens: 7, OutputTokens: 2}}
		case 2:
			close(p.taskStarted)
			<-ctx.Done()
		case 3:
			ch <- llm.StreamChunk{Content: "Work completed"}
			ch <- llm.StreamChunk{Done: true, Usage: &llm.Usage{InputTokens: 11, OutputTokens: 5}}
		}
	}()
	return ch, nil
}

func TestTaskExecutorMissionSelectsMissionScopedRunner(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Storage: &config.StorageConfig{Backend: "sqlite", Path: ":memory:"},
		Models:  []config.Model{{Name: "test", Provider: config.ProviderOpenAICompatible, BaseURL: "http://localhost", Aliases: map[string]string{"fast": "fast"}}},
		Missions: []config.Mission{
			{
				Name: "executor_policy", Commander: &config.MissionCommander{Model: "fast"},
				Models: []string{"fast"},
				Tasks: []config.Task{
					{Name: "work", RawObjective: "Do the work"},
				},
			},
		},
	}

	runner, err := NewRunner(cfg, "", "executor_policy", nil)
	if err != nil {
		t.Fatalf("NewRunner() rejected mission-scoped mission: %v", err)
	}
	defer runner.CloseStores()
	if !runner.mission.HasExecutorPolicy() {
		t.Fatal("runner lost executor policy")
	}
}

func TestMissionScopedRunnerCompletesThroughOneCommander(t *testing.T) {
	provider := newMockProvider(
		mockToolCall("start_task", json.RawMessage(`{"task_name":"work","model":"fast"}`)),
		mockResponse{Content: "Work completed"},
	)
	cfg := &config.Config{
		Storage: &config.StorageConfig{Backend: "sqlite", Path: ":memory:"},
		Models:  []config.Model{{Name: "test", Provider: config.ProviderOpenAICompatible, BaseURL: "http://localhost", Aliases: map[string]string{"fast": "fast"}}},
		Missions: []config.Mission{{
			Name: "executor_policy", Directive: "Complete the declared work",
			Commander: &config.MissionCommander{Model: "fast"}, Models: []string{"fast"},
			Tasks: []config.Task{{Name: "work", RawObjective: "Do the work"}},
		}},
	}
	runner, err := NewRunner(cfg, "", "executor_policy", nil, WithProviderFactory(func() llm.Provider { return provider }))
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	defer runner.CloseStores()
	streamer := newMockMissionStreamer()
	if err := runner.Run(context.Background(), streamer); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !streamer.hasEvent("mission_completed") || streamer.eventCount("task_completed") != 1 {
		t.Fatalf("stream events = %#v", streamer.getEvents())
	}
	restored, _, err := missionruntime.RestoreDurableHarness(runner.stores.Runtime, runner.missionID)
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	if snapshot := restored.Snapshot(); snapshot.RunState != missionruntime.RunCompleted || snapshot.Commander.ID != runner.missionID+":commander" {
		t.Fatalf("restored snapshot = %#v", snapshot)
	}
	if result, ok := restored.TaskResult("work"); !ok || result.Envelope.Summary != "Work completed" {
		t.Fatalf("task result = %#v, %v", result, ok)
	}
}

func TestMissionScopedRunnerExecutesInlineIteration(t *testing.T) {
	provider := newMockProvider(
		mockToolCall("start_task", json.RawMessage(`{"task_name":"work[0]","model":"fast"}`)),
		mockResponse{Content: "First item complete"},
		mockToolCall("start_task", json.RawMessage(`{"task_name":"work[1]","model":"fast"}`)),
		mockResponse{Content: "Second item complete"},
	)
	cfg := &config.Config{
		Storage: &config.StorageConfig{Backend: "sqlite", Path: ":memory:"},
		Models:  []config.Model{{Name: "test", Provider: config.ProviderOpenAICompatible, BaseURL: "http://localhost", Aliases: map[string]string{"fast": "fast"}}},
		Missions: []config.Mission{{
			Name: "iterate_inline", Commander: &config.MissionCommander{Model: "fast"}, Models: []string{"fast"},
			Datasets: []config.Dataset{{Name: "items", Items: []cty.Value{cty.StringVal("first"), cty.StringVal("second")}}},
			Tasks:    []config.Task{{Name: "work", RawObjective: "Process the supplied item", Iterator: &config.TaskIterator{Dataset: "items"}}},
		}},
	}
	runner, err := NewRunner(cfg, "", "iterate_inline", nil, WithProviderFactory(func() llm.Provider { return provider }))
	if err != nil {
		t.Fatal(err)
	}
	defer runner.CloseStores()
	if err := runner.Run(context.Background(), newMockMissionStreamer()); err != nil {
		t.Fatal(err)
	}
	restored, _, err := missionruntime.RestoreDurableHarness(runner.stores.Runtime, runner.missionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := restored.Snapshot()
	if snapshot.RunState != missionruntime.RunCompleted || snapshot.Tasks["work"].State != missionruntime.TaskCompleted || len(snapshot.IterationPlans["work"].Children) != 2 {
		t.Fatalf("iterated mission snapshot = %#v", snapshot)
	}
	if len(snapshot.Active) != 0 {
		t.Fatalf("active tasks after completion = %#v", snapshot.Active)
	}
	for index, want := range []string{`"first"`, `"second"`} {
		name := fmt.Sprintf("work[%d]", index)
		result, ok := restored.TaskResult(name)
		if !ok || result.Envelope.Status != missionruntime.ResultSucceeded {
			t.Fatalf("child %s result = %#v, %v", name, result, ok)
		}
		if got := string(snapshot.IterationPlans["work"].Children[index].Item); got != want {
			t.Fatalf("child %s frozen item = %s, want %s", name, got, want)
		}
	}
}

func TestMissionScopedRunnerIteratesDatasetProducedByTaskWorker(t *testing.T) {
	provider := newMockProvider(
		mockToolCall("start_task", json.RawMessage(`{"task_name":"load","model":"fast"}`)),
		mockToolCall("builtins_dataset_set", json.RawMessage(`{"name":"items","items":[{"label":"alpha"},{"label":"beta"}]}`)),
		mockResponse{Content: "Dataset populated"},
		mockToolCall("start_task", json.RawMessage(`{"task_name":"work[0]","model":"fast"}`)),
		mockResponse{Content: "Processed alpha"},
		mockToolCall("start_task", json.RawMessage(`{"task_name":"work[1]","model":"fast"}`)),
		mockResponse{Content: "Processed beta"},
	)
	cfg := &config.Config{
		Storage: &config.StorageConfig{Backend: "sqlite", Path: ":memory:"},
		Models:  []config.Model{{Name: "test", Provider: config.ProviderOpenAICompatible, BaseURL: "http://localhost", Aliases: map[string]string{"fast": "fast"}}},
		Missions: []config.Mission{{
			Name: "iterate_produced", Commander: &config.MissionCommander{Model: "fast"}, Models: []string{"fast"},
			Datasets: []config.Dataset{{Name: "items"}},
			Tasks: []config.Task{
				{Name: "load", RawObjective: "Populate the items dataset", Tools: []string{"builtins.dataset.set"}},
				{Name: "work", RawObjective: "Process the supplied item", DependsOn: []string{"load"}, Iterator: &config.TaskIterator{Dataset: "items"}},
			},
		}},
	}
	runner, err := NewRunner(cfg, "", "iterate_produced", nil, WithProviderFactory(func() llm.Provider { return provider }))
	if err != nil {
		t.Fatal(err)
	}
	defer runner.CloseStores()
	if err := runner.Run(context.Background(), newMockMissionStreamer()); err != nil {
		t.Fatal(err)
	}
	restored, _, err := missionruntime.RestoreDurableHarness(runner.stores.Runtime, runner.missionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(restored.Snapshot().IterationPlans["work"].Children); got != 2 {
		t.Fatalf("frozen children after dataset tool = %d, want 2", got)
	}
	if state := restored.Snapshot().RunState; state != missionruntime.RunCompleted {
		t.Fatalf("run state = %q", state)
	}
}

func TestRuntimeDatasetInitializationRepairsPendingRunAfterRestart(t *testing.T) {
	storagePath := filepath.Join(t.TempDir(), "pending-iteration.db")
	cfg := &config.Config{
		Storage: &config.StorageConfig{Backend: "sqlite", Path: storagePath},
		Models:  []config.Model{{Name: "test", Provider: config.ProviderOpenAICompatible, BaseURL: "http://localhost", Aliases: map[string]string{"fast": "fast"}}},
		Missions: []config.Mission{{
			Name: "pending_iteration", Commander: &config.MissionCommander{Model: "fast"}, Models: []string{"fast"},
			Datasets: []config.Dataset{{Name: "items", Items: []cty.Value{cty.StringVal("first"), cty.StringVal("second")}}},
			Tasks:    []config.Task{{Name: "work", RawObjective: "Process item", Iterator: &config.TaskIterator{Dataset: "items"}}},
		}},
	}
	first, err := NewRunner(cfg, "", "pending_iteration", nil)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := first.createMissionRecord(`{}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := RuntimeTaskDefinitions(first.mission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missionruntime.CreateDurableHarness(first.stores.Runtime, runID, runID+":commander", definitions); err != nil {
		t.Fatal(err)
	}
	first.CloseStores() // Simulate a crash before any dataset was created.

	resumed, err := NewRunner(cfg, "", "pending_iteration", nil, WithResume(runID))
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.CloseStores()
	if err := resumed.restoreRuntimeInputs(); err != nil {
		t.Fatal(err)
	}
	resumed.missionID = runID
	if err := resumed.initializeRuntimeDatasets(true); err != nil {
		t.Fatal(err)
	}
	id := resumed.datasetIDs["items"]
	count, err := resumed.stores.Datasets.GetItemCount(id)
	if err != nil || count != 2 {
		t.Fatalf("repaired dataset count = %d, %v", count, err)
	}
	// A second startup cannot duplicate the initial items.
	if err := resumed.initializeRuntimeDatasets(true); err != nil {
		t.Fatal(err)
	}
	count, err = resumed.stores.Datasets.GetItemCount(id)
	if err != nil || count != 2 {
		t.Fatalf("replayed initialization count = %d, %v", count, err)
	}
}

func TestMissionScopedRunnerRestoresCrossMissionLaunchIntent(t *testing.T) {
	provider := newMockProvider(
		mockToolCall("start_task", json.RawMessage(`{"task_name":"classify","model":"fast"}`)),
		mockResponse{Content: "This needs follow-up."},
		mockToolCall("select_route", json.RawMessage(`{"task_name":"classify","target":"follow_up","inputs":{"topic":"durability"}}`)),
	)
	storagePath := filepath.Join(t.TempDir(), "cross-mission.db")
	cfg := &config.Config{
		Storage: &config.StorageConfig{Backend: "sqlite", Path: storagePath},
		Models:  []config.Model{{Name: "test", Provider: config.ProviderOpenAICompatible, BaseURL: "http://localhost", Aliases: map[string]string{"fast": "fast"}}},
		Missions: []config.Mission{
			{
				Name: "route_source", Commander: &config.MissionCommander{Model: "fast"}, Models: []string{"fast"},
				Tasks: []config.Task{{Name: "classify", RawObjective: "Classify", Router: &config.TaskRouter{Routes: []config.TaskRoute{{Target: "follow_up", Condition: "needs follow-up", IsMission: true}}}}},
			},
			{
				Name: "follow_up", Commander: &config.MissionCommander{Model: "fast"}, Models: []string{"fast"},
				Inputs: []config.MissionInput{{Name: "topic", Type: "string"}},
				Tasks:  []config.Task{{Name: "work", RawObjective: "Follow up"}},
			},
		},
	}
	first, err := NewRunner(cfg, "", "route_source", nil, WithProviderFactory(func() llm.Provider { return provider }))
	if err != nil {
		t.Fatalf("NewRunner(first) error = %v", err)
	}
	if err := first.Run(context.Background(), newMockMissionStreamer()); err != nil {
		t.Fatalf("Run(first) error = %v", err)
	}
	runID := first.missionID
	if first.NextMission() != "follow_up" || first.NextMissionInputs()["topic"] != "durability" {
		t.Fatalf("launch intent = %q %#v", first.NextMission(), first.NextMissionInputs())
	}
	if err := first.AcknowledgeNextMissionLaunch("child-run-1"); err != nil {
		t.Fatalf("AcknowledgeNextMissionLaunch() error = %v", err)
	}
	first.CloseStores()

	resumed, err := NewRunner(cfg, "", "route_source", nil, WithResume(runID), WithProviderFactory(func() llm.Provider { return provider }))
	if err != nil {
		t.Fatalf("NewRunner(resumed) error = %v", err)
	}
	defer resumed.CloseStores()
	if err := resumed.Run(context.Background(), newMockMissionStreamer()); err != nil {
		t.Fatalf("Run(resumed) error = %v", err)
	}
	if resumed.NextMission() != "" || resumed.NextMissionInputs() != nil {
		t.Fatalf("acknowledged launch was replayed = %q %#v", resumed.NextMission(), resumed.NextMissionInputs())
	}
	if calls := provider.callCount(); calls != 3 {
		t.Fatalf("provider calls after replay = %d, want 3", calls)
	}
}
