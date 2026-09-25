package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"squadron/store"
)

func TestCoordinatorPersistsRecoveryWaitAndResumesSameAttempt(t *testing.T) {
	t.Parallel()
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-wait", "commander-wait", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	executor := &recoveryWaitingExecutor{}
	coordinator, err := NewCoordinator(harness, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	err = coordinator.Run(context.Background())
	var waiting *MissionRecoveryWaitingError
	if !errors.As(err, &waiting) || len(waiting.Blocks) != 1 {
		t.Fatalf("Run() error = %v, want one recovery block", err)
	}
	blockedSnapshot := harness.Snapshot()
	block := waiting.Blocks[0]
	if blockedSnapshot.RunState != RunWaiting || block.AttemptID == "" {
		t.Fatalf("blocked snapshot/error = %#v / %#v", blockedSnapshot, waiting)
	}
	if err := harness.ResolveRecoveryBlock(block.AttemptID, "reconciled", false); err != nil {
		t.Fatalf("ResolveRecoveryBlock() error = %v", err)
	}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	final := harness.Snapshot()
	if final.RunState != RunCompleted || final.Tasks["work"].Attempt != 1 || executor.calls != 2 {
		t.Fatalf("final snapshot/calls = %#v / %d", final, executor.calls)
	}
}

func TestCoordinatorParksHumanInputWithoutHoldingExecutionLease(t *testing.T) {
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-human-coordinator", "commander-human", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	executor := &humanWaitingExecutor{}
	coordinator, err := NewCoordinator(harness, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatal(err)
	}
	err = coordinator.Run(context.Background())
	var waiting *MissionHumanInputWaitingError
	if !errors.As(err, &waiting) || len(waiting.Waits) != 1 {
		t.Fatalf("Run() error = %v, want one human input wait", err)
	}
	wait := waiting.Waits[0]
	if snapshot := harness.Snapshot(); snapshot.RunState != RunWaiting || snapshot.Tasks["work"].State != TaskWaiting || len(snapshot.Active) != 0 {
		t.Fatalf("waiting snapshot = %#v", snapshot)
	}
	if _, leased := harness.inner.Checkpoint().ExecutionLeases[wait.AttemptID]; leased {
		t.Fatal("human input wait retained execution lease")
	}
	restored, _, err := RestoreDurableHarness(runtimeStore, "run-human-coordinator")
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.ResolveHumanInputWait(wait.AttemptID, wait.ToolCallID, "yes", "user-1", false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	resumed, err := NewCoordinator(restored, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snapshot := restored.Snapshot(); snapshot.RunState != RunCompleted || snapshot.Tasks["work"].Attempt != 1 || executor.calls != 2 {
		t.Fatalf("completed snapshot/calls = %#v / %d", snapshot, executor.calls)
	}
}

func TestHumanInputWaitDoesNotConsumeParallelTaskCapacity(t *testing.T) {
	harness, err := CreateDurableHarnessWithMaxParallel(store.NewMemoryRuntimeStore(), "run-parallel-human", "commander", []TaskDefinition{
		{Name: "ask"}, {Name: "other"},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewCoordinator(harness, &readyCommander{}, &humanWaitingExecutor{waitTask: "ask"}, staticGrantResolver{})
	if err != nil {
		t.Fatal(err)
	}
	err = coordinator.Run(context.Background())
	var waiting *MissionHumanInputWaitingError
	if !errors.As(err, &waiting) {
		t.Fatalf("Run() error = %v, want human input wait", err)
	}
	snapshot := harness.Snapshot()
	if snapshot.Tasks["ask"].State != TaskWaiting || snapshot.Tasks["other"].State != TaskCompleted || snapshot.RunState != RunWaiting {
		t.Fatalf("unrelated task was blocked by human input: %#v", snapshot)
	}
}

type humanWaitingExecutor struct {
	calls    int
	waitTask string
}

func (*humanWaitingExecutor) Execute(context.Context, Dispatch) (TaskResultEnvelope, error) {
	return TaskResultEnvelope{}, fmt.Errorf("unexpected non-resumable execution")
}

func (e *humanWaitingExecutor) ExecuteResumable(_ context.Context, dispatch Dispatch, progress TaskAttemptProgress, commit func(TaskAttemptProgress) error) (TaskResultEnvelope, error) {
	e.calls++
	if e.waitTask != "" && dispatch.TaskName != e.waitTask {
		return testResult(dispatch), nil
	}
	if operation, ok := progress.Tools["call-1"]; ok && operation.Status == ToolOperationCompleted {
		return testResult(dispatch), nil
	}
	progress.Phase = "tool_calls_received"
	progress.Tools = map[string]ToolOperation{"call-1": {
		ToolUseID: "call-1", ToolName: "builtins.human.ask", InputJSON: `{"question":"Proceed?"}`,
		Status: ToolOperationInFlight, StartedAt: time.Now().UTC(),
	}}
	if err := commit(progress); err != nil {
		return TaskResultEnvelope{}, err
	}
	return TaskResultEnvelope{}, &TaskHumanInputWaitingError{Wait: HumanInputWait{
		AttemptID: dispatch.AttemptID, TaskName: dispatch.TaskName, ToolUseID: "call-1", ToolName: "builtins.human.ask",
		ToolCallID: "stable-call", Question: "Proceed?", RequestedAt: time.Now().UTC(),
	}}
}

func TestCoordinatorReclaimsExpiredLeaseAndResumesSameAttempt(t *testing.T) {
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-crash-recovery", "commander-crash-recovery", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := harness.Snapshot()
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "dispatch-work", TaskName: "work", ReadySetRevision: snapshot.Revision, Grant: testGrant("work")})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	if _, err := harness.AcquireExecutionLease(dispatch.AttemptID, "dead-worker", "dead-token", time.Now().UTC(), 40*time.Millisecond); err != nil {
		t.Fatalf("AcquireExecutionLease(dead) error = %v", err)
	}
	restored, _, err := RestoreDurableHarness(runtimeStore, "run-crash-recovery")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	executor := &recordingExecutor{}
	coordinator, err := NewCoordinator(restored, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if err := coordinator.SetExecutionLeasePolicy("replacement-worker", 250*time.Millisecond, 50*time.Millisecond); err != nil {
		t.Fatalf("SetExecutionLeasePolicy() error = %v", err)
	}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	final := restored.Snapshot()
	if final.RunState != RunCompleted || final.Tasks["work"].Attempt != 1 || !reflect.DeepEqual(executor.tasks, []string{"work"}) {
		t.Fatalf("recovered snapshot/tasks = %#v / %v", final, executor.tasks)
	}
}

func TestCoordinatorCancellationReleasesLeaseWithoutFailingAttempt(t *testing.T) {
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-graceful-stop", "commander-graceful-stop", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	executor := &cancellationExecutor{started: make(chan struct{})}
	coordinator, err := NewCoordinator(harness, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx) }()
	<-executor.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context canceled", err)
	}
	checkpoint := harness.inner.Checkpoint()
	active := harness.Snapshot().Active
	if len(active) != 1 || harness.Snapshot().Tasks["work"].State != TaskRunning {
		t.Fatalf("canceled attempt was not left active: %#v", harness.Snapshot())
	}
	if _, leased := checkpoint.ExecutionLeases[active[0].AttemptID]; leased {
		t.Fatalf("graceful cancellation retained lease: %#v", checkpoint.ExecutionLeases)
	}
}

type cancellationExecutor struct{ started chan struct{} }

func (e *cancellationExecutor) Execute(ctx context.Context, _ Dispatch) (TaskResultEnvelope, error) {
	close(e.started)
	<-ctx.Done()
	return TaskResultEnvelope{}, ctx.Err()
}

type recoveryWaitingExecutor struct{ calls int }

func (e *recoveryWaitingExecutor) Execute(context.Context, Dispatch) (TaskResultEnvelope, error) {
	return TaskResultEnvelope{}, fmt.Errorf("unexpected non-resumable execution")
}

func (e *recoveryWaitingExecutor) ExecuteResumable(_ context.Context, dispatch Dispatch, progress TaskAttemptProgress, commit func(TaskAttemptProgress) error) (TaskResultEnvelope, error) {
	e.calls++
	if operation, ok := progress.Tools["call-1"]; ok && operation.Status == ToolOperationCompleted {
		return testResult(dispatch), nil
	}
	progress.Phase = "tool_calls_received"
	progress.Tools = map[string]ToolOperation{"call-1": {ToolUseID: "call-1", ToolName: "charge", InputJSON: `{}`, Status: ToolOperationInFlight}}
	if err := commit(progress); err != nil {
		return TaskResultEnvelope{}, err
	}
	return TaskResultEnvelope{}, &TaskRecoveryBlockedError{
		Err: fmt.Errorf("outcome unknown"), ToolUseID: "call-1", ToolName: "charge", IdempotencyKey: "key-1",
	}
}

func TestCoordinatorCompletesLinearGraphWithOneCommanderDriver(t *testing.T) {
	t.Parallel()

	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-1", "commander-1", []TaskDefinition{
		{Name: "collect"},
		{Name: "analyze", DependsOn: []string{"collect"}},
		{Name: "publish", DependsOn: []string{"analyze"}},
	})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	commander := &linearCommander{}
	executor := &recordingExecutor{}
	coordinator, err := NewCoordinator(harness, commander, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if commander.turns != 3 {
		t.Fatalf("commander turns = %d, want 3", commander.turns)
	}
	if !reflect.DeepEqual(executor.tasks, []string{"collect", "analyze", "publish"}) {
		t.Fatalf("executed tasks = %v", executor.tasks)
	}

	restored, _, err := RestoreDurableHarness(runtimeStore, "run-1")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	if snapshot := restored.Snapshot(); snapshot.RunState != RunCompleted || snapshot.Commander.ID != "commander-1" {
		t.Fatalf("restored snapshot = %#v", snapshot)
	}
	if len(restored.CommanderMessages()) != 9 {
		t.Fatalf("commander message count = %d, want 9", len(restored.CommanderMessages()))
	}
}

func TestCoordinatorRunsFanOutConcurrentlyThenJoins(t *testing.T) {
	t.Parallel()

	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarnessWithMaxParallel(runtimeStore, "run-fanout", "commander-fanout", []TaskDefinition{
		{Name: "left"}, {Name: "right"}, {Name: "join", DependsOn: []string{"left", "right"}},
	}, 2)
	if err != nil {
		t.Fatalf("CreateDurableHarnessWithMaxParallel() error = %v", err)
	}
	commander := &readyCommander{}
	executor := &concurrencyExecutor{}
	coordinator, err := NewCoordinator(harness, commander, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if executor.maxActive != 2 {
		t.Fatalf("maximum active executors = %d, want 2", executor.maxActive)
	}
	if snapshot := harness.Snapshot(); snapshot.RunState != RunCompleted || len(snapshot.Results) != 3 {
		t.Fatalf("final snapshot = %#v", snapshot)
	}
	if commander.turns != 3 {
		t.Fatalf("commander turns = %d, want 3", commander.turns)
	}
}

func TestCoordinatorPersistsFailureAndRetriesWithANewAttempt(t *testing.T) {
	t.Parallel()

	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-retry", "commander-retry", []TaskDefinition{{Name: "unstable", MaxRetries: 1}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	commander := &retryCommander{}
	executor := &failOnceExecutor{}
	coordinator, err := NewCoordinator(harness, commander, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	snapshot := harness.Snapshot()
	if snapshot.RunState != RunCompleted || snapshot.Tasks["unstable"].Attempt != 2 {
		t.Fatalf("final snapshot = %#v", snapshot)
	}
	if _, exists := snapshot.Failures["unstable"]; exists {
		t.Fatalf("successful retry retained stale failure: %#v", snapshot.Failures)
	}
	_, events, err := RestoreDurableHarness(runtimeStore, "run-retry")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	var sawFailure, sawRetry bool
	for _, event := range events {
		if event.Type == EventTaskAttemptFailed {
			sawFailure = event.Failure != nil && event.Failure.Error == "injected executor failure"
		}
		sawRetry = sawRetry || event.Type == EventTaskRetryScheduled
	}
	if !sawFailure || !sawRetry {
		t.Fatalf("failure events missing: %#v", events)
	}
}

func TestCoordinatorPersistsRouteDecisionAndExecutesOnlySelectedBranch(t *testing.T) {
	t.Parallel()
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-route", "commander-route", []TaskDefinition{
		{Name: "classify", Routes: []RouteDefinition{{Target: "billing", Condition: "billing"}, {Target: "support", Condition: "support"}}},
		{Name: "billing"}, {Name: "support"},
	})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	commander := &routingCommander{}
	executor := &recordingExecutor{}
	coordinator, err := NewCoordinator(harness, commander, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(executor.tasks, []string{"classify", "support"}) {
		t.Fatalf("executed tasks = %v", executor.tasks)
	}
	restored, events, err := RestoreDurableHarness(runtimeStore, "run-route")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	if restored.Snapshot().RouteSelections["classify"].Target != "support" {
		t.Fatalf("restored routes = %#v", restored.Snapshot().RouteSelections)
	}
	found, inspected := false, false
	for _, event := range events {
		if event.Type == EventRouteSelected && event.Route != nil && event.Route.Target == "support" {
			found = true
		}
		inspected = inspected || event.Type == EventTaskInspected
	}
	if !found || !inspected {
		t.Fatalf("route event missing: %#v", events)
	}
}

type routingCommander struct {
	turns     int
	inspected bool
}

func (c *routingCommander) Decide(_ context.Context, snapshot Snapshot, _ []CommanderMessage) (CommanderDecision, error) {
	c.turns++
	id := fmt.Sprintf("route-turn-%d", c.turns)
	base := CommanderDecision{
		Prompt: CommanderMessage{ID: id + ":prompt", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: "snapshot"}}},
	}
	if len(snapshot.PendingRoutes) > 0 {
		if !c.inspected {
			base.Message = CommanderMessage{ID: id, Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: id, ToolName: "inspect_task"}}}
			base.Inspect = InspectTaskRequest{RequestID: id, TaskName: "classify", Revision: snapshot.Revision}
			return base, nil
		}
		base.Message = CommanderMessage{ID: id, Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: id, ToolName: "select_route"}}}
		base.Route = RouteRequest{RequestID: id, TaskName: "classify", Target: "support", Revision: snapshot.Revision}
		return base, nil
	}
	taskName := snapshot.Ready[0]
	base.Message = CommanderMessage{ID: id, Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: id, ToolName: "start_task"}}}
	base.Dispatch = DispatchRequest{RequestID: id, TaskName: taskName, ReadySetRevision: snapshot.Revision}
	base.Grant = GrantRequest{TaskName: taskName, Model: "test-model"}
	return base, nil
}
func (*routingCommander) DispatchAccepted(Dispatch)    {}
func (*routingCommander) RouteAccepted(RouteSelection) {}
func (c *routingCommander) InspectionAccepted(inspection TaskInspection) {
	c.inspected = inspection.TaskName == "classify"
}

func TestCoordinatorFailsMissionWhenRetryPolicyIsExhausted(t *testing.T) {
	t.Parallel()

	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-failed", "commander-failed", []TaskDefinition{{Name: "broken", MaxRetries: 1}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	coordinator, err := NewCoordinator(harness, &retryCommander{}, alwaysFailExecutor{}, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if err := coordinator.Run(context.Background()); err == nil {
		t.Fatal("Run() succeeded after retry policy was exhausted")
	}
	snapshot := harness.Snapshot()
	if snapshot.RunState != RunFailed || snapshot.Commander.State != CommanderFailed || snapshot.Tasks["broken"].State != TaskFailed {
		t.Fatalf("failed snapshot = %#v", snapshot)
	}
	failure := snapshot.Failures["broken"]
	if failure.Attempt != 2 || failure.Error != "injected executor failure" {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestCoordinatorRetriesRejectedResultEnvelope(t *testing.T) {
	t.Parallel()

	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-rejected", "commander-rejected", []TaskDefinition{{Name: "structured", MaxRetries: 1}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	executor := &rejectOnceExecutor{}
	coordinator, err := NewCoordinator(harness, &retryCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if snapshot := harness.Snapshot(); snapshot.RunState != RunCompleted || snapshot.Tasks["structured"].Attempt != 2 {
		t.Fatalf("final snapshot = %#v", snapshot)
	}
}

type retryCommander struct{ turns int }

func (c *retryCommander) Decide(_ context.Context, snapshot Snapshot, _ []CommanderMessage) (CommanderDecision, error) {
	c.turns++
	taskName := snapshot.Ready[0]
	requestID := fmt.Sprintf("retry-dispatch-%d", c.turns)
	return CommanderDecision{
		Prompt: CommanderMessage{ID: requestID + ":prompt", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: "snapshot"}}},
		Message: CommanderMessage{ID: requestID + ":message", Role: "assistant", Parts: []store.MessagePart{{
			Type: "tool_use", ToolUseID: requestID, ToolName: "start_task", ToolInputJSON: fmt.Sprintf(`{"task_name":%q}`, taskName),
		}}},
		Dispatch: DispatchRequest{RequestID: requestID, TaskName: taskName, ReadySetRevision: snapshot.Revision},
		Grant:    GrantRequest{TaskName: taskName, Model: "test-model"},
	}, nil
}

func (*retryCommander) DispatchAccepted(Dispatch)         {}
func (*retryCommander) RouteAccepted(RouteSelection)      {}
func (*retryCommander) InspectionAccepted(TaskInspection) {}

type failOnceExecutor struct{ attempts int }

func (e *failOnceExecutor) Execute(_ context.Context, dispatch Dispatch) (TaskResultEnvelope, error) {
	e.attempts++
	if e.attempts == 1 {
		return TaskResultEnvelope{}, fmt.Errorf("injected executor failure")
	}
	return testResult(dispatch), nil
}

type alwaysFailExecutor struct{}

func (alwaysFailExecutor) Execute(context.Context, Dispatch) (TaskResultEnvelope, error) {
	return TaskResultEnvelope{}, fmt.Errorf("injected executor failure")
}

type rejectOnceExecutor struct{ attempts int }

func (e *rejectOnceExecutor) Execute(_ context.Context, dispatch Dispatch) (TaskResultEnvelope, error) {
	e.attempts++
	if e.attempts == 1 {
		result := testResult(dispatch)
		result.Summary = ""
		return result, nil
	}
	return testResult(dispatch), nil
}

type readyCommander struct{ turns int }

func (c *readyCommander) Decide(_ context.Context, snapshot Snapshot, _ []CommanderMessage) (CommanderDecision, error) {
	c.turns++
	if len(snapshot.Ready) == 0 {
		return CommanderDecision{}, fmt.Errorf("no ready task")
	}
	taskName := snapshot.Ready[0]
	return CommanderDecision{
		Prompt:   CommanderMessage{ID: fmt.Sprintf("fanout-%d:prompt", c.turns), Role: "user", Parts: []store.MessagePart{{Type: "text", Text: "snapshot"}}},
		Message:  CommanderMessage{ID: fmt.Sprintf("fanout-%d", c.turns), Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: "fanout-" + taskName, ToolName: "start_task", ToolInputJSON: fmt.Sprintf(`{"task_name":%q}`, taskName)}}},
		Dispatch: DispatchRequest{RequestID: "fanout-" + taskName, TaskName: taskName, ReadySetRevision: snapshot.Revision},
		Grant:    GrantRequest{TaskName: taskName, Model: "test-model"},
	}, nil
}
func (*readyCommander) DispatchAccepted(Dispatch)         {}
func (*readyCommander) RouteAccepted(RouteSelection)      {}
func (*readyCommander) InspectionAccepted(TaskInspection) {}

type concurrencyExecutor struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (e *concurrencyExecutor) Execute(_ context.Context, dispatch Dispatch) (TaskResultEnvelope, error) {
	e.mu.Lock()
	e.active++
	if e.active > e.maxActive {
		e.maxActive = e.active
	}
	e.mu.Unlock()
	time.Sleep(25 * time.Millisecond)
	e.mu.Lock()
	e.active--
	e.mu.Unlock()
	return testResult(dispatch), nil
}

type linearCommander struct{ turns int }

func (c *linearCommander) Decide(_ context.Context, snapshot Snapshot, _ []CommanderMessage) (CommanderDecision, error) {
	c.turns++
	if len(snapshot.Ready) != 1 {
		return CommanderDecision{}, fmt.Errorf("expected exactly one ready task, got %v", snapshot.Ready)
	}
	taskName := snapshot.Ready[0]
	return CommanderDecision{
		Prompt: CommanderMessage{
			ID: fmt.Sprintf("turn-%d:prompt", c.turns), Role: "user",
			Parts: []store.MessagePart{{Type: "text", Text: "Current mission snapshot"}},
		},
		Message: CommanderMessage{
			ID: fmt.Sprintf("turn-%d", c.turns), Role: "assistant",
			Parts: []store.MessagePart{{
				Type: "tool_use", ToolUseID: "dispatch-" + taskName, ToolName: "start_task",
				ToolInputJSON: fmt.Sprintf(`{"task_name":%q}`, taskName),
			}},
		},
		Dispatch: DispatchRequest{
			RequestID: "dispatch-" + taskName, TaskName: taskName, ReadySetRevision: snapshot.Revision,
		},
		Grant: GrantRequest{TaskName: taskName, Model: "test-model"},
	}, nil
}

func (c *linearCommander) DispatchAccepted(Dispatch)         {}
func (c *linearCommander) RouteAccepted(RouteSelection)      {}
func (c *linearCommander) InspectionAccepted(TaskInspection) {}

type recordingExecutor struct{ tasks []string }

func (e *recordingExecutor) Execute(_ context.Context, dispatch Dispatch) (TaskResultEnvelope, error) {
	e.tasks = append(e.tasks, dispatch.TaskName)
	return testResult(dispatch), nil
}

type staticGrantResolver struct{}

func (staticGrantResolver) ResolveExecutionGrant(request GrantRequest) (ExecutionGrant, error) {
	return ExecutionGrant{
		TaskName: request.TaskName, Model: "test-model", RunAs: "user:test", ConfigurationVersion: "test-config",
	}, nil
}
