package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	persistence "squadron/store"
)

type fixedIterationPlanResolver struct{ plan IterationPlan }

func (r fixedIterationPlanResolver) ResolveIterationPlan(_ context.Context, name string) (IterationPlan, error) {
	if name != r.plan.ParentTask {
		return IterationPlan{}, fmt.Errorf("unexpected iteration task %q", name)
	}
	return r.plan, nil
}

func TestCoordinatorRunsIteratedChildrenThroughDurableHarness(t *testing.T) {
	policy := IterationPolicy{Dataset: "items"}
	h, err := CreateDurableHarness(persistence.NewMemoryRuntimeStore(), "run-coordinated-iteration", "commander", []TaskDefinition{{Name: "work", Objective: "Process one item", Iterator: &policy}})
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	coordinator, err := NewCoordinator(h, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.SetIterationPlanResolver(fixedIterationPlanResolver{plan: iterationTestPlan(t, policy, `1`, `2`)}, []string{"work"})
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(executor.tasks, []string{"work[0]", "work[1]"}) || h.Snapshot().RunState != RunCompleted {
		t.Fatalf("executed tasks = %v, state = %q", executor.tasks, h.Snapshot().RunState)
	}
}

func iterationTestPlan(t *testing.T, policy IterationPolicy, items ...string) IterationPlan {
	t.Helper()
	raw := make([]json.RawMessage, len(items))
	for i, item := range items {
		raw[i] = json.RawMessage(item)
	}
	plan, err := BuildIterationPlan("work", policy, raw)
	if err != nil {
		t.Fatalf("BuildIterationPlan() error = %v", err)
	}
	return plan
}

func TestDurableIterationSequentialRecoveryAndParentCompletion(t *testing.T) {
	policy := IterationPolicy{Dataset: "items"}
	store := persistence.NewMemoryRuntimeStore()
	h, err := CreateDurableHarness(store, "run-iteration", "commander-iteration", []TaskDefinition{
		{Name: "work", Objective: "Process one item", Iterator: &policy},
		{Name: "finish", DependsOn: []string{"work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	plan := iterationTestPlan(t, policy, `{"id":1}`, `{"id":2}`)
	if _, err := h.DispatchTask(DispatchRequest{RequestID: "parent", TaskName: "work", ReadySetRevision: h.Snapshot().Revision, Grant: testGrant("work")}); err == nil || !strings.Contains(err.Error(), "expand") {
		t.Fatalf("parent dispatch error = %v", err)
	}
	if err := h.ExpandIteration(plan); err != nil {
		t.Fatal(err)
	}
	if err := h.ExpandIteration(plan); err != nil {
		t.Fatalf("identical expansion replay failed: %v", err)
	}
	if ready := h.Snapshot().Ready; !reflect.DeepEqual(ready, []string{"work[0]"}) {
		t.Fatalf("initial ready set = %v", ready)
	}
	restored, events, err := RestoreDurableHarness(store, "run-iteration")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Snapshot(), h.Snapshot()) {
		t.Fatal("iteration plan or child states changed on restore")
	}
	expansions := 0
	for _, event := range events {
		if event.Type == EventIterationExpanded {
			expansions++
		}
	}
	if expansions != 1 {
		t.Fatalf("expansion events = %d, want 1", expansions)
	}
	for _, name := range []string{"work[0]", "work[1]"} {
		snapshot := restored.Snapshot()
		if !reflect.DeepEqual(snapshot.Ready, []string{name}) {
			t.Fatalf("ready set before %s = %v", name, snapshot.Ready)
		}
		dispatch, err := restored.DispatchTask(DispatchRequest{RequestID: name, TaskName: name, ReadySetRevision: snapshot.Revision, Grant: testGrant(name)})
		if err != nil {
			t.Fatal(err)
		}
		if err := restored.CompleteTask(name, dispatch.AttemptID, testResult(dispatch)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := restored.Snapshot()
	if snapshot.Tasks["work"].State != TaskCompleted || !reflect.DeepEqual(snapshot.Ready, []string{"finish"}) {
		t.Fatalf("parent completion did not release downstream task: %#v", snapshot)
	}
	result, ok := restored.TaskResult("work")
	if !ok || result.Envelope.Summary != "Completed 2 iteration items" {
		t.Fatalf("parent result = %#v, %v", result, ok)
	}
}

func TestDurableIterationParallelLimitAndEmptyDataset(t *testing.T) {
	policy := IterationPolicy{Dataset: "items", Parallel: true, ConcurrencyLimit: 1}
	h, err := CreateDurableHarnessWithMaxParallel(persistence.NewMemoryRuntimeStore(), "run-parallel", "commander", []TaskDefinition{{Name: "work", Iterator: &policy}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if err := h.ExpandIteration(iterationTestPlan(t, policy, `1`, `2`)); err != nil {
		t.Fatal(err)
	}
	snapshot := h.Snapshot()
	first, err := h.DispatchTask(DispatchRequest{RequestID: "first", TaskName: "work[0]", ReadySetRevision: snapshot.Revision, Grant: testGrant("work[0]")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.DispatchTask(DispatchRequest{RequestID: "second", TaskName: "work[1]", ReadySetRevision: h.Snapshot().Revision, Grant: testGrant("work[1]")}); err == nil || !strings.Contains(err.Error(), "parallel children") {
		t.Fatalf("parallel limit error = %v", err)
	}
	if err := h.CompleteTask(first.TaskName, first.AttemptID, testResult(first)); err != nil {
		t.Fatal(err)
	}
	second, err := h.DispatchTask(DispatchRequest{RequestID: "second", TaskName: "work[1]", ReadySetRevision: h.Snapshot().Revision, Grant: testGrant("work[1]")})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.CompleteTask(second.TaskName, second.AttemptID, testResult(second)); err != nil {
		t.Fatal(err)
	}
	if h.Snapshot().RunState != RunCompleted {
		t.Fatalf("parallel iteration state = %q", h.Snapshot().RunState)
	}

	empty, err := CreateDurableHarness(persistence.NewMemoryRuntimeStore(), "run-empty", "commander", []TaskDefinition{{Name: "work", Iterator: &policy}})
	if err != nil {
		t.Fatal(err)
	}
	if err := empty.Start(); err != nil {
		t.Fatal(err)
	}
	if err := empty.ExpandIteration(iterationTestPlan(t, policy)); err != nil {
		t.Fatal(err)
	}
	if empty.Snapshot().RunState != RunCompleted || empty.Snapshot().Tasks["work"].State != TaskCompleted {
		t.Fatalf("empty iteration = %#v", empty.Snapshot())
	}
}

func TestSQLiteIterationPlanAndChildAttemptSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iteration.db")
	policy := IterationPolicy{Dataset: "items", Parallel: true, ConcurrencyLimit: 2, Smoketest: true}
	bundle := openSQLiteBundle(t, path)
	h, err := CreateDurableHarnessWithMaxParallel(bundle.Runtime, "run-sqlite-iteration", "commander", []TaskDefinition{{Name: "work", Iterator: &policy}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	plan := iterationTestPlan(t, policy, `{"id":1}`, `{"id":2}`)
	if err := h.ExpandIteration(plan); err != nil {
		t.Fatal(err)
	}
	snapshot := h.Snapshot()
	first, err := h.DispatchTask(DispatchRequest{RequestID: "first", TaskName: "work[0]", ReadySetRevision: snapshot.Revision, Grant: testGrant("work[0]")})
	if err != nil {
		t.Fatal(err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, restored, _ := reopenSQLiteHarness(t, path, "run-sqlite-iteration")
	defer closeSQLiteBundle(t, bundle)
	if got := restored.Snapshot(); got.Tasks["work[0]"].State != TaskRunning || got.Tasks["work[1]"].State != TaskBlocked || !reflect.DeepEqual(got.IterationPlans["work"], plan) {
		t.Fatalf("restored iteration = %#v", got)
	}
	if err := restored.CompleteTask("work[0]", first.AttemptID, testResult(first)); err != nil {
		t.Fatal(err)
	}
	snapshot = restored.Snapshot()
	if !reflect.DeepEqual(snapshot.Ready, []string{"work[1]"}) {
		t.Fatalf("smoketest did not release second item: %v", snapshot.Ready)
	}
}

type timedIterationExecutor struct {
	mu     sync.Mutex
	starts map[string]time.Time
}

func (e *timedIterationExecutor) Execute(_ context.Context, dispatch Dispatch) (TaskResultEnvelope, error) {
	e.mu.Lock()
	e.starts[dispatch.TaskName] = time.Now()
	e.mu.Unlock()
	return testResult(dispatch), nil
}

func TestIterationStartDelayIsPersistedAndEnforcedAfterRestore(t *testing.T) {
	policy := IterationPolicy{Dataset: "items", Parallel: true, ConcurrencyLimit: 3, StartDelayMS: 60}
	runtimeStore := persistence.NewMemoryRuntimeStore()
	h, err := CreateDurableHarnessWithMaxParallel(runtimeStore, "run-stagger", "commander", []TaskDefinition{{Name: "work", Iterator: &policy}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if err := h.ExpandIteration(iterationTestPlan(t, policy, `1`, `2`, `3`)); err != nil {
		t.Fatal(err)
	}
	for index := range 3 {
		name := fmt.Sprintf("work[%d]", index)
		if _, err := h.DispatchTask(DispatchRequest{RequestID: name, TaskName: name, ReadySetRevision: h.Snapshot().Revision, Grant: testGrant(name)}); err != nil {
			t.Fatal(err)
		}
	}
	restored, _, err := RestoreDurableHarness(runtimeStore, "run-stagger")
	if err != nil {
		t.Fatal(err)
	}
	active := restored.Snapshot().Active
	for index := 1; index < len(active); index++ {
		if delay := active[index].StartNotBefore.Sub(active[index-1].StartNotBefore); delay < 50*time.Millisecond {
			t.Fatalf("persisted start spacing %d = %v", index, delay)
		}
	}
	executor := &timedIterationExecutor{starts: make(map[string]time.Time)}
	coordinator, err := NewCoordinator(restored, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.executeConcurrent(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	for index := 1; index < 3; index++ {
		previous, current := fmt.Sprintf("work[%d]", index-1), fmt.Sprintf("work[%d]", index)
		if gap := executor.starts[current].Sub(executor.starts[previous]); gap < 40*time.Millisecond {
			t.Fatalf("actual start gap %s -> %s = %v", previous, current, gap)
		}
	}
}

func TestIterationChildRetryKeepsFrozenItemAndCompletesParent(t *testing.T) {
	policy := IterationPolicy{Dataset: "items", MaxRetries: 1}
	runtimeStore := persistence.NewMemoryRuntimeStore()
	h, err := CreateDurableHarness(runtimeStore, "run-iteration-retry", "commander", []TaskDefinition{{Name: "work", Iterator: &policy}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if err := h.ExpandIteration(iterationTestPlan(t, policy, `{"id":"stable"}`)); err != nil {
		t.Fatal(err)
	}
	first, err := h.DispatchTask(DispatchRequest{RequestID: "first", TaskName: "work[0]", ReadySetRevision: h.Snapshot().Revision, Grant: testGrant("work[0]")})
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := h.FailTask("work[0]", first.AttemptID, fmt.Errorf("transient")); err != nil || !retry {
		t.Fatalf("FailTask() = %v, %v", retry, err)
	}
	restored, _, err := RestoreDurableHarness(runtimeStore, "run-iteration-retry")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Snapshot().Tasks["work"].State != TaskWaiting {
		t.Fatal("parent completed before child retry")
	}
	second, err := restored.DispatchTask(DispatchRequest{RequestID: "second", TaskName: "work[0]", ReadySetRevision: restored.Snapshot().Revision, Grant: testGrant("work[0]")})
	if err != nil {
		t.Fatal(err)
	}
	if second.Attempt != 2 || second.Grant.Iteration == nil || string(second.Grant.Iteration.Item) != `{"id":"stable"}` {
		t.Fatalf("retried child = %#v", second)
	}
	if err := restored.CompleteTask("work[0]", second.AttemptID, testResult(second)); err != nil {
		t.Fatal(err)
	}
	if restored.Snapshot().RunState != RunCompleted || restored.Snapshot().Tasks["work"].State != TaskCompleted {
		t.Fatalf("retry completion = %#v", restored.Snapshot())
	}
}
