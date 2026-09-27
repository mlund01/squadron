package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"squadron/store"
)

func TestAttemptDeadlinePersistsAndRenewsOnlyForANewAttempt(t *testing.T) {
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-deadline", "commander", []TaskDefinition{{Name: "work", MaxRetries: 1, TimeoutSeconds: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	first, err := harness.DispatchTask(DispatchRequest{RequestID: "first", TaskName: "work", ReadySetRevision: harness.Snapshot().Revision, Grant: testGrant("work")})
	if err != nil {
		t.Fatal(err)
	}
	if !first.DeadlineAt.Equal(first.DispatchedAt.Add(5 * time.Second)) {
		t.Fatalf("first deadline = %s, dispatched at %s", first.DeadlineAt, first.DispatchedAt)
	}
	restored, _, err := RestoreDurableHarness(runtimeStore, "run-deadline")
	if err != nil {
		t.Fatal(err)
	}
	if active := restored.Snapshot().Active; len(active) != 1 || !active[0].DeadlineAt.Equal(first.DeadlineAt) {
		t.Fatalf("restored active dispatch = %#v", active)
	}
	if retry, err := restored.FailTask("work", first.AttemptID, errors.New("first attempt failed")); err != nil || !retry {
		t.Fatalf("FailTask() retry=%t, err=%v", retry, err)
	}
	second, err := restored.DispatchTask(DispatchRequest{RequestID: "second", TaskName: "work", ReadySetRevision: restored.Snapshot().Revision, Grant: testGrant("work")})
	if err != nil {
		t.Fatal(err)
	}
	if second.AttemptID == first.AttemptID || !second.DeadlineAt.After(first.DeadlineAt) {
		t.Fatalf("retry did not receive a fresh deadline: first=%#v second=%#v", first, second)
	}
}

type deadlineExecutor struct {
	calls int
	runs  []Dispatch
}

func (e *deadlineExecutor) Execute(ctx context.Context, dispatch Dispatch) (TaskResultEnvelope, error) {
	e.calls++
	e.runs = append(e.runs, dispatch)
	<-ctx.Done()
	// A late success must not commit once the attempt's deadline has passed.
	return testResult(dispatch), nil
}

func TestCoordinatorTimeoutFailsTaskAndMission(t *testing.T) {
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-timeout", "commander", []TaskDefinition{{Name: "work", TimeoutSeconds: 1}})
	if err != nil {
		t.Fatal(err)
	}
	executor := &deadlineExecutor{}
	coordinator, err := NewCoordinator(harness, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatal(err)
	}
	err = coordinator.Run(context.Background())
	var timedOut *TaskAttemptTimeoutError
	if !errors.As(err, &timedOut) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want task failure distinct from mission cancellation", err)
	}
	snapshot := harness.Snapshot()
	if snapshot.RunState != RunFailed || snapshot.Tasks["work"].State != TaskFailed || executor.calls != 1 {
		t.Fatalf("snapshot/calls = %#v / %d", snapshot, executor.calls)
	}
	if !timedOut.DeadlineAt.Equal(executor.runs[0].DeadlineAt) {
		t.Fatalf("timeout deadline %s differs from persisted dispatch %s", timedOut.DeadlineAt, executor.runs[0].DeadlineAt)
	}
}

type timeoutThenSuccessExecutor struct{ runs []Dispatch }

func (e *timeoutThenSuccessExecutor) Execute(ctx context.Context, dispatch Dispatch) (TaskResultEnvelope, error) {
	e.runs = append(e.runs, dispatch)
	if len(e.runs) == 1 {
		<-ctx.Done()
		return TaskResultEnvelope{}, ctx.Err()
	}
	return testResult(dispatch), nil
}

func TestCoordinatorTimeoutRetryGetsFreshDeadline(t *testing.T) {
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-timeout-retry", "commander", []TaskDefinition{{Name: "work", MaxRetries: 1, TimeoutSeconds: 1}})
	if err != nil {
		t.Fatal(err)
	}
	executor := &timeoutThenSuccessExecutor{}
	coordinator, err := NewCoordinator(harness, &retryCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(executor.runs) != 2 || !executor.runs[1].DeadlineAt.After(executor.runs[0].DeadlineAt) {
		t.Fatalf("retry deadlines = %#v", executor.runs)
	}
	if snapshot := harness.Snapshot(); snapshot.RunState != RunCompleted || snapshot.Tasks["work"].Attempt != 2 {
		t.Fatalf("completed snapshot = %#v", snapshot)
	}
}

func TestExpiredAttemptDoesNotRestartExecutorAfterRecovery(t *testing.T) {
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-expired", "commander", []TaskDefinition{{Name: "work", TimeoutSeconds: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "first", TaskName: "work", ReadySetRevision: harness.Snapshot().Revision, Grant: testGrant("work")})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(dispatch.DeadlineAt) + 5*time.Millisecond)
	restored, _, err := RestoreDurableHarness(runtimeStore, "run-expired")
	if err != nil {
		t.Fatal(err)
	}
	executor := &deadlineExecutor{}
	coordinator, err := NewCoordinator(restored, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatal(err)
	}
	var timedOut *TaskAttemptTimeoutError
	if err := coordinator.Run(context.Background()); !errors.As(err, &timedOut) {
		t.Fatalf("recovered Run() = %v, want timeout", err)
	}
	if executor.calls != 0 || restored.Snapshot().RunState != RunFailed {
		t.Fatalf("expired attempt was re-executed: calls=%d state=%s", executor.calls, restored.Snapshot().RunState)
	}
}
