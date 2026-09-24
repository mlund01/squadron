package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	persistence "squadron/store"
)

func TestSQLiteRestartAfterPersistedToolResultDoesNotRepeatSideEffect(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "runtime.db")
	const runID = "run-restart-tool-result"
	bundle := openSQLiteBundle(t, dbPath)
	harness, err := CreateDurableHarness(bundle.Runtime, runID, "commander-tool-result", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	executor := &crashAfterToolResultExecutor{cancel: cancel}
	coordinator, err := NewCoordinator(harness, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if err := coordinator.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Run() error = %v, want context canceled", err)
	}
	activeBeforeRestart := harness.Snapshot().Active
	if len(activeBeforeRestart) != 1 {
		t.Fatalf("active attempts before restart = %#v", activeBeforeRestart)
	}
	if executor.effects != 1 {
		t.Fatalf("side effects before restart = %d, want 1", executor.effects)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, eventsBeforeResume := reopenSQLiteHarness(t, dbPath, runID)
	restoredProgress, ok := harness.TaskProgress(activeBeforeRestart[0].AttemptID)
	if !ok || restoredProgress.Tools["call-1"].Status != ToolOperationCompleted {
		t.Fatalf("restored tool progress = %#v, %v", restoredProgress, ok)
	}
	executor.cancel = nil
	coordinator, err = NewCoordinator(harness, &readyCommander{}, executor, staticGrantResolver{})
	if err != nil {
		t.Fatalf("NewCoordinator(restored) error = %v", err)
	}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if executor.effects != 1 {
		t.Fatalf("side effects after restart = %d, want 1", executor.effects)
	}
	final := harness.Snapshot()
	if final.RunState != RunCompleted || final.Tasks["work"].Attempt != 1 {
		t.Fatalf("final snapshot = %#v", final)
	}
	closeSQLiteBundle(t, bundle)

	bundle, restored, eventsAfterResume := reopenSQLiteHarness(t, dbPath, runID)
	closeSQLiteBundle(t, bundle)
	if restored.Snapshot().RunState != RunCompleted {
		t.Fatalf("restored final state = %q", restored.Snapshot().RunState)
	}
	if len(eventsAfterResume) <= len(eventsBeforeResume) {
		t.Fatalf("resume event count = %d, want more than %d", len(eventsAfterResume), len(eventsBeforeResume))
	}
}

type crashAfterToolResultExecutor struct {
	cancel  context.CancelFunc
	effects int
}

func TestSQLiteRestartPreservesParallelAttemptsAndClarificationSession(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "runtime.db")
	const runID = "run-restart-parallel-clarification"
	bundle := openSQLiteBundle(t, dbPath)
	harness, err := CreateDurableHarnessWithMaxParallel(bundle.Runtime, runID, "commander-parallel", []TaskDefinition{
		{Name: "left", Routes: []RouteDefinition{{Target: "follow_up", Condition: "needs more work", IsMission: true}}},
		{Name: "right"},
	}, 2)
	if err != nil {
		t.Fatalf("CreateDurableHarnessWithMaxParallel() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	left, err := harness.DispatchTask(DispatchRequest{
		RequestID: "dispatch-left", TaskName: "left", ReadySetRevision: harness.Snapshot().Revision, Grant: testGrant("left"),
	})
	if err != nil {
		t.Fatalf("DispatchTask(left) error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, _ = reopenSQLiteHarness(t, dbPath, runID)
	right, err := harness.DispatchTask(DispatchRequest{
		RequestID: "dispatch-right", TaskName: "right", ReadySetRevision: harness.Snapshot().Revision, Grant: testGrant("right"),
	})
	if err != nil {
		t.Fatalf("DispatchTask(right) error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, _ = reopenSQLiteHarness(t, dbPath, runID)
	active := harness.Snapshot().Active
	if len(active) != 2 || active[0].AttemptID == active[1].AttemptID {
		t.Fatalf("restored parallel attempts = %#v", active)
	}
	leftResult := testResult(left)
	leftResult.SessionTranscript = []TaskSessionMessage{
		{Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: "left objective"}}},
		{Role: "assistant", Parts: []persistence.MessagePart{{Type: "text", Text: "left evidence"}}},
	}
	if err := harness.CompleteTask("left", left.AttemptID, leftResult); err != nil {
		t.Fatalf("CompleteTask(left) error = %v", err)
	}
	request := ClarifyTaskRequest{
		RequestID: "clarify-left", MessageID: "clarify-left-message", TaskName: "left",
		Question: "Which evidence was decisive?", Revision: harness.Snapshot().Revision,
	}
	prompt := CommanderMessage{ID: "clarify-left-prompt", Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: "clarify"}}}
	message := CommanderMessage{ID: request.MessageID, Role: "assistant", Parts: []persistence.MessagePart{{Type: "tool_use", ToolUseID: request.RequestID, ToolName: "clarify_task_result"}}}
	if err := harness.BeginCommanderClarification(prompt, message, request); err != nil {
		t.Fatalf("BeginCommanderClarification() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, events := reopenSQLiteHarness(t, dbPath, runID)
	eventsAfterClarificationRequest := len(events)
	pending := harness.Snapshot().PendingClarifications
	if len(pending) != 1 || pending[0].RequestID != request.RequestID {
		t.Fatalf("restored pending clarification = %#v", pending)
	}
	if active = harness.Snapshot().Active; len(active) != 1 || active[0].AttemptID != right.AttemptID {
		t.Fatalf("restored remaining parallel attempt = %#v, want %q", active, right.AttemptID)
	}
	if err := harness.BeginCommanderClarification(prompt, message, request); err != nil {
		t.Fatalf("replayed BeginCommanderClarification() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)
	assertSQLiteEventCount(t, dbPath, runID, eventsAfterClarificationRequest)

	bundle, harness, _ = reopenSQLiteHarness(t, dbPath, runID)
	_, _, transcript, err := harness.ClarificationContext(request.RequestID)
	if err != nil {
		t.Fatalf("ClarificationContext() error = %v", err)
	}
	updatedTranscript := append(transcript,
		TaskSessionMessage{Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: request.Question}}},
		TaskSessionMessage{Role: "assistant", Parts: []persistence.MessagePart{{Type: "text", Text: "left evidence"}}},
	)
	clarification, err := harness.CompleteClarification(request.RequestID, request.MessageID, TaskClarificationResult{
		Answer: []persistence.MessagePart{{Type: "text", Text: "left evidence"}}, SessionTranscript: updatedTranscript,
	})
	if err != nil {
		t.Fatalf("CompleteClarification() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, events = reopenSQLiteHarness(t, dbPath, runID)
	eventsAfterClarification := len(events)
	replayedClarification, err := harness.CompleteClarification(request.RequestID, request.MessageID, TaskClarificationResult{
		Answer: []persistence.MessagePart{{Type: "text", Text: "different"}},
	})
	if err != nil || !reflect.DeepEqual(replayedClarification, clarification) {
		t.Fatalf("replayed clarification = %#v, %v; want %#v", replayedClarification, err, clarification)
	}
	routePrompt := CommanderMessage{ID: "left-route-prompt", Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: "route left"}}}
	routeMessage := CommanderMessage{ID: "left-route-message", Role: "assistant", Parts: []persistence.MessagePart{{Type: "tool_use", ToolUseID: "left-route-none", ToolName: "select_route"}}}
	if _, err := harness.ApplyCommanderRoute(routePrompt, routeMessage, RouteRequest{
		RequestID: "left-route-none", TaskName: "left", Target: "none", Revision: harness.Snapshot().Revision,
	}); err != nil {
		t.Fatalf("ApplyCommanderRoute(left) error = %v", err)
	}
	if err := harness.CompleteTask("right", right.AttemptID, testResult(right)); err != nil {
		t.Fatalf("CompleteTask(right) error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, finalEvents := reopenSQLiteHarness(t, dbPath, runID)
	closeSQLiteBundle(t, bundle)
	if harness.Snapshot().RunState != RunCompleted || harness.Snapshot().Tasks["left"].Attempt != 1 || harness.Snapshot().Tasks["right"].Attempt != 1 {
		t.Fatalf("restored final parallel snapshot = %#v", harness.Snapshot())
	}
	if len(finalEvents) <= eventsAfterClarification {
		t.Fatalf("final events = %d, want more than %d", len(finalEvents), eventsAfterClarification)
	}
}

func (e *crashAfterToolResultExecutor) Execute(context.Context, Dispatch) (TaskResultEnvelope, error) {
	return TaskResultEnvelope{}, errors.New("expected resumable execution")
}

func (e *crashAfterToolResultExecutor) ExecuteResumable(ctx context.Context, dispatch Dispatch, progress TaskAttemptProgress, commit func(TaskAttemptProgress) error) (TaskResultEnvelope, error) {
	if operation, ok := progress.Tools["call-1"]; ok && operation.Status == ToolOperationCompleted {
		return testResult(dispatch), nil
	}
	progress.Phase = "tool_calls_received"
	progress.Tools = map[string]ToolOperation{"call-1": {
		ToolUseID: "call-1", ToolName: "external_write", InputJSON: `{"value":"once"}`, Status: ToolOperationIntended,
	}}
	if err := commit(progress); err != nil {
		return TaskResultEnvelope{}, err
	}
	operation := progress.Tools["call-1"]
	operation.Status = ToolOperationInFlight
	progress.Tools["call-1"] = operation
	if err := commit(progress); err != nil {
		return TaskResultEnvelope{}, err
	}
	e.effects++
	operation.Status = ToolOperationCompleted
	operation.Result = "effect committed"
	progress.Tools["call-1"] = operation
	progress.Phase = "final_response"
	if err := commit(progress); err != nil {
		return TaskResultEnvelope{}, err
	}
	if e.cancel != nil {
		e.cancel()
		return TaskResultEnvelope{}, ctx.Err()
	}
	return testResult(dispatch), nil
}

// TestSQLiteRestartRecoveryAcrossDurableBoundaries exercises the same
// persistence boundary used in production while repeatedly closing the
// database, reopening it, and replaying the last request. A replay must retain
// the original identities and must not append events or create another child
// mission.
func TestSQLiteRestartRecoveryAcrossDurableBoundaries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "runtime.db")
	const runID = "run-restart-boundaries"
	definitions := []TaskDefinition{{
		Name: "classify",
		Routes: []RouteDefinition{{
			Target: "follow_up", Condition: "needs follow-up", IsMission: true,
			Inputs: []RouteInputDefinition{{Name: "topic", Type: "string", Required: true}},
		}},
	}}

	bundle := openSQLiteBundle(t, dbPath)
	harness, err := CreateDurableHarness(bundle.Runtime, runID, "commander-restart", definitions)
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, events := reopenSQLiteHarness(t, dbPath, runID)
	if len(events) != 1 {
		t.Fatalf("creation event count = %d, want 1", len(events))
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, events = reopenSQLiteHarness(t, dbPath, runID)
	eventsAfterStart := len(events)
	prompt := CommanderMessage{ID: "dispatch-prompt", Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: "start classify"}}}
	message := CommanderMessage{ID: "dispatch-message", Role: "assistant", Parts: []persistence.MessagePart{{Type: "tool_use", ToolUseID: "dispatch-classify", ToolName: "start_task"}}}
	request := DispatchRequest{
		RequestID: "dispatch-classify", TaskName: "classify",
		ReadySetRevision: harness.Snapshot().Revision, Grant: testGrant("classify"),
	}
	dispatch, err := harness.ApplyCommanderDispatch(prompt, message, request)
	if err != nil {
		t.Fatalf("ApplyCommanderDispatch() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, events = reopenSQLiteHarness(t, dbPath, runID)
	if len(events) <= eventsAfterStart {
		t.Fatalf("dispatch did not append events: before=%d after=%d", eventsAfterStart, len(events))
	}
	eventsAfterDispatch := len(events)
	replayedDispatch, err := harness.ApplyCommanderDispatch(prompt, message, request)
	if err != nil {
		t.Fatalf("replayed ApplyCommanderDispatch() error = %v", err)
	}
	if !reflect.DeepEqual(replayedDispatch, dispatch) {
		t.Fatalf("replayed dispatch = %#v, want %#v", replayedDispatch, dispatch)
	}
	conflictingPrompt := prompt
	conflictingPrompt.Parts = []persistence.MessagePart{{Type: "text", Text: "different dispatch"}}
	if _, err := harness.ApplyCommanderDispatch(conflictingPrompt, message, request); err == nil {
		t.Fatal("ApplyCommanderDispatch() accepted conflicting replay transcript")
	}
	closeSQLiteBundle(t, bundle)
	assertSQLiteEventCount(t, dbPath, runID, eventsAfterDispatch)

	bundle, harness, _ = reopenSQLiteHarness(t, dbPath, runID)
	progress := TaskAttemptProgress{
		AttemptID: dispatch.AttemptID,
		Phase:     "tool_calls_received",
		Transcript: []TaskSessionMessage{{Role: "assistant", Parts: []persistence.MessagePart{{
			Type: "tool_use", ToolUseID: "call-1", ToolName: "lookup", ToolInputJSON: `{"topic":"durability"}`,
		}}}},
		Tools: map[string]ToolOperation{"call-1": {
			ToolUseID: "call-1", ToolName: "lookup", InputJSON: `{"topic":"durability"}`, Status: ToolOperationIntended,
		}},
	}
	if err := harness.SaveTaskProgress(dispatch, progress); err != nil {
		t.Fatalf("SaveTaskProgress(intended) error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, events = reopenSQLiteHarness(t, dbPath, runID)
	eventsAfterIntent := len(events)
	if err := harness.SaveTaskProgress(dispatch, progress); err != nil {
		t.Fatalf("replayed SaveTaskProgress(intended) error = %v", err)
	}
	closeSQLiteBundle(t, bundle)
	assertSQLiteEventCount(t, dbPath, runID, eventsAfterIntent)

	bundle, harness, _ = reopenSQLiteHarness(t, dbPath, runID)
	operation := progress.Tools["call-1"]
	operation.Status = ToolOperationInFlight
	progress.Tools["call-1"] = operation
	if err := harness.SaveTaskProgress(dispatch, progress); err != nil {
		t.Fatalf("SaveTaskProgress(in-flight) error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, _ = reopenSQLiteHarness(t, dbPath, runID)
	operation = progress.Tools["call-1"]
	operation.Status = ToolOperationCompleted
	operation.Result = "persisted result"
	progress.Tools["call-1"] = operation
	progress.Phase = "final_response"
	if err := harness.SaveTaskProgress(dispatch, progress); err != nil {
		t.Fatalf("SaveTaskProgress(completed) error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, _ = reopenSQLiteHarness(t, dbPath, runID)
	restoredProgress, ok := harness.TaskProgress(dispatch.AttemptID)
	if !ok || !reflect.DeepEqual(restoredProgress, progress) {
		t.Fatalf("restored progress = %#v, %v; want %#v", restoredProgress, ok, progress)
	}
	result := testResult(dispatch)
	result.SessionTranscript = progress.Transcript
	if err := harness.CompleteTask("classify", dispatch.AttemptID, result); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, _ = reopenSQLiteHarness(t, dbPath, runID)
	routePrompt := CommanderMessage{ID: "route-prompt", Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: "select route"}}}
	routeMessage := CommanderMessage{ID: "route-message", Role: "assistant", Parts: []persistence.MessagePart{{Type: "tool_use", ToolUseID: "route-follow-up", ToolName: "select_route"}}}
	routeRequest := RouteRequest{
		RequestID: "route-follow-up", TaskName: "classify", Target: "follow_up",
		Inputs: map[string]string{"topic": "durability"}, Revision: harness.Snapshot().Revision,
	}
	selection, err := harness.ApplyCommanderRoute(routePrompt, routeMessage, routeRequest)
	if err != nil {
		t.Fatalf("ApplyCommanderRoute() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, events = reopenSQLiteHarness(t, dbPath, runID)
	eventsAfterRoute := len(events)
	replayedSelection, err := harness.ApplyCommanderRoute(routePrompt, routeMessage, routeRequest)
	if err != nil {
		t.Fatalf("replayed ApplyCommanderRoute() error = %v", err)
	}
	if !reflect.DeepEqual(replayedSelection, selection) {
		t.Fatalf("replayed route = %#v, want %#v", replayedSelection, selection)
	}
	conflictingRoutePrompt := routePrompt
	conflictingRoutePrompt.Parts = []persistence.MessagePart{{Type: "text", Text: "different route"}}
	if _, err := harness.ApplyCommanderRoute(conflictingRoutePrompt, routeMessage, routeRequest); err == nil {
		t.Fatal("ApplyCommanderRoute() accepted conflicting replay transcript")
	}
	closeSQLiteBundle(t, bundle)
	assertSQLiteEventCount(t, dbPath, runID, eventsAfterRoute)

	bundle, harness, _ = reopenSQLiteHarness(t, dbPath, runID)
	childRunID, err := bundle.Missions.CreateMissionForLaunch(runID, selection.RequestID, selection.Target, `{"topic":"durability"}`, `{"mission":"follow_up"}`)
	if err != nil {
		t.Fatalf("CreateMissionForLaunch() error = %v", err)
	}
	if _, err := harness.CompleteCrossMissionLaunch(selection.RequestID, childRunID); err != nil {
		t.Fatalf("CompleteCrossMissionLaunch() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)

	bundle, harness, events = reopenSQLiteHarness(t, dbPath, runID)
	eventsAfterLaunch := len(events)
	replayedChildRunID, err := bundle.Missions.CreateMissionForLaunch(runID, selection.RequestID, selection.Target, `{"topic":"durability"}`, `{"mission":"follow_up"}`)
	if err != nil {
		t.Fatalf("replayed CreateMissionForLaunch() error = %v", err)
	}
	if replayedChildRunID != childRunID {
		t.Fatalf("replayed child run ID = %q, want %q", replayedChildRunID, childRunID)
	}
	launch, err := harness.CompleteCrossMissionLaunch(selection.RequestID, childRunID)
	if err != nil {
		t.Fatalf("replayed CompleteCrossMissionLaunch() error = %v", err)
	}
	if launch.State != CrossMissionLaunchLaunched || launch.ChildRunID != childRunID {
		t.Fatalf("restored launch = %#v", launch)
	}
	closeSQLiteBundle(t, bundle)
	assertSQLiteEventCount(t, dbPath, runID, eventsAfterLaunch)

	bundle = openSQLiteBundle(t, dbPath)
	missions, total, err := bundle.Missions.ListMissions(100, 0)
	if err != nil {
		t.Fatalf("ListMissions() error = %v", err)
	}
	closeSQLiteBundle(t, bundle)
	if total != 1 || len(missions) != 1 || missions[0].ID != childRunID {
		t.Fatalf("child missions = %#v, total=%d; want exactly %q", missions, total, childRunID)
	}
}

func openSQLiteBundle(t *testing.T, dbPath string) *persistence.Bundle {
	t.Helper()
	bundle, err := persistence.NewSQLiteBundle(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteBundle() error = %v", err)
	}
	return bundle
}

func closeSQLiteBundle(t *testing.T, bundle *persistence.Bundle) {
	t.Helper()
	if err := bundle.Close(); err != nil {
		t.Fatalf("Bundle.Close() error = %v", err)
	}
}

func reopenSQLiteHarness(t *testing.T, dbPath, runID string) (*persistence.Bundle, *DurableHarness, []Event) {
	t.Helper()
	bundle := openSQLiteBundle(t, dbPath)
	harness, events, err := RestoreDurableHarness(bundle.Runtime, runID)
	if err != nil {
		closeSQLiteBundle(t, bundle)
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	return bundle, harness, events
}

func assertSQLiteEventCount(t *testing.T, dbPath, runID string, want int) {
	t.Helper()
	bundle, _, events := reopenSQLiteHarness(t, dbPath, runID)
	closeSQLiteBundle(t, bundle)
	if len(events) != want {
		t.Fatalf("event count after replay = %d, want %d", len(events), want)
	}
}
