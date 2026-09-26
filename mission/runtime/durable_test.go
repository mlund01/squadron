package runtime

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	persistence "squadron/store"
)

func TestExecutionLeaseTakeoverFencesStaleWorkerWrites(t *testing.T) {
	runtimeStore := persistence.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-lease-fence", "commander-lease-fence", []TaskDefinition{{Name: "work"}})
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
	now := time.Now().UTC()
	if _, err := harness.AcquireExecutionLease(dispatch.AttemptID, "old-worker", "old-token", now, time.Second); err != nil {
		t.Fatalf("AcquireExecutionLease(old) error = %v", err)
	}
	if _, err := harness.AcquireExecutionLease(dispatch.AttemptID, "new-worker", "new-token", now.Add(2*time.Second), time.Second); err != nil {
		t.Fatalf("AcquireExecutionLease(takeover) error = %v", err)
	}
	progress, _ := harness.TaskProgress(dispatch.AttemptID)
	progress.Phase = "stale-write"
	if err := harness.SaveTaskProgressWithLease(dispatch, progress, "old-token"); err == nil {
		t.Fatal("stale worker progress write succeeded after takeover")
	} else {
		var lost *ExecutionLeaseLostError
		if !errors.As(err, &lost) {
			t.Fatalf("stale progress error = %T %v, want ExecutionLeaseLostError", err, err)
		}
	}
	if err := harness.CompleteTaskWithLease(dispatch.TaskName, dispatch.AttemptID, testResult(dispatch), "new-token"); err != nil {
		t.Fatalf("CompleteTaskWithLease(new) error = %v", err)
	}
	if state := harness.Snapshot().RunState; state != RunCompleted {
		t.Fatalf("run state = %q, want completed", state)
	}
}

func TestDurableHarnessPersistsOrchestrationFailure(t *testing.T) {
	t.Parallel()
	runtimeStore := persistence.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-commander-failed", "commander-failed", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	if err := harness.FailRun(errors.New("commander provider unavailable")); err != nil {
		t.Fatal(err)
	}
	restored, events, err := RestoreDurableHarness(runtimeStore, "run-commander-failed")
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot(); got.RunState != RunFailed || got.Commander.State != CommanderFailed {
		t.Fatalf("restored failure state = %#v", got)
	}
	last := events[len(events)-1]
	if last.Type != EventMissionFailed || last.Error != "commander provider unavailable" {
		t.Fatalf("failure event = %#v", last)
	}
	if err := restored.FailRun(errors.New("duplicate")); err != nil {
		t.Fatal(err)
	}
	_, repeatedEvents, err := RestoreDurableHarness(runtimeStore, "run-commander-failed")
	if err != nil {
		t.Fatal(err)
	}
	if len(repeatedEvents) != len(events) {
		t.Fatalf("duplicate failure appended events: %d, want %d", len(repeatedEvents), len(events))
	}
}

func TestDurableHarnessRestoresCommanderAndDispatchHistory(t *testing.T) {
	t.Parallel()

	store := persistence.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(store, "run-1", "commander-1", []TaskDefinition{
		{Name: "collect"},
		{Name: "publish", DependsOn: []string{"collect"}},
	})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	message := CommanderMessage{
		ID:   "message-1",
		Role: "assistant",
		Parts: []persistence.MessagePart{
			{Type: "text", Text: "I will start with collect."},
			{Type: "provider_raw", ProviderName: "squadron", ProviderType: "control", ProviderDataJSON: `{"task":"collect"}`},
		},
	}
	if err := harness.AppendCommanderMessage(message); err != nil {
		t.Fatalf("AppendCommanderMessage() error = %v", err)
	}
	started := harness.Snapshot()
	dispatch, err := harness.DispatchTask(DispatchRequest{
		RequestID:        "dispatch-collect",
		TaskName:         "collect",
		ReadySetRevision: started.Revision,
		Grant:            testGrant("collect"),
	})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}

	restored, events, err := RestoreDurableHarness(store, "run-1")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	if !reflect.DeepEqual(restored.Snapshot(), harness.Snapshot()) {
		t.Fatalf("restored snapshot = %#v, want %#v", restored.Snapshot(), harness.Snapshot())
	}
	if restored.Snapshot().Commander.ID != "commander-1" {
		t.Fatalf("restored commander = %#v", restored.Snapshot().Commander)
	}
	if !reflect.DeepEqual(restored.CommanderMessages(), []CommanderMessage{message}) {
		t.Fatalf("restored messages = %#v", restored.CommanderMessages())
	}
	if len(events) != 6 {
		t.Fatalf("event count = %d, want 6", len(events))
	}

	repeated, err := restored.DispatchTask(DispatchRequest{
		RequestID:        "dispatch-collect",
		TaskName:         "collect",
		ReadySetRevision: started.Revision,
	})
	if err != nil || !reflect.DeepEqual(repeated, dispatch) {
		t.Fatalf("restored idempotent dispatch = %#v, %v; want %#v", repeated, err, dispatch)
	}
}

func TestDurableClarificationRestoresPendingRequestAndCommitsUpdatedSession(t *testing.T) {
	t.Parallel()
	runtimeStore := persistence.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-clarify", "commander-clarify", []TaskDefinition{
		{Name: "classify", Routes: []RouteDefinition{{Target: "support", Condition: "authentication failure"}}},
		{Name: "support"},
	})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := harness.Snapshot()
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "dispatch-classify", TaskName: "classify", ReadySetRevision: snapshot.Revision, Grant: testGrant("classify")})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	result := testResult(dispatch)
	result.SessionTranscript = []TaskSessionMessage{
		{Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: "classify request"}}},
		{Role: "assistant", Parts: []persistence.MessagePart{{Type: "text", Text: "authentication issue"}}},
	}
	if err := harness.CompleteTask("classify", dispatch.AttemptID, result); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	snapshot = harness.Snapshot()
	request := ClarifyTaskRequest{RequestID: "clarify-1", MessageID: "clarify-message", TaskName: "classify", Question: "Which status code supports that?", Revision: snapshot.Revision}
	prompt := CommanderMessage{ID: "clarify-prompt", Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: "snapshot"}}}
	message := CommanderMessage{ID: "clarify-message", Role: "assistant", Parts: []persistence.MessagePart{{Type: "tool_use", ToolUseID: "clarify-1", ToolName: "clarify_task_result"}}}
	if err := harness.BeginCommanderClarification(prompt, message, request); err != nil {
		t.Fatalf("BeginCommanderClarification() error = %v", err)
	}
	_, eventsAfterBegin, err := RestoreDurableHarness(runtimeStore, "run-clarify")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() after begin error = %v", err)
	}
	if err := harness.BeginCommanderClarification(prompt, message, request); err != nil {
		t.Fatalf("idempotent BeginCommanderClarification() error = %v", err)
	}
	_, eventsAfterBeginReplay, err := RestoreDurableHarness(runtimeStore, "run-clarify")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() after begin replay error = %v", err)
	}
	if len(eventsAfterBeginReplay) != len(eventsAfterBegin) {
		t.Fatalf("begin replay appended events: before=%d after=%d", len(eventsAfterBegin), len(eventsAfterBeginReplay))
	}

	restored, _, err := RestoreDurableHarness(runtimeStore, "run-clarify")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	if pending := restored.Snapshot().PendingClarifications; len(pending) != 1 || pending[0].RequestID != "clarify-1" {
		t.Fatalf("restored pending clarifications = %#v", pending)
	}
	loadedRequest, loadedDispatch, transcript, err := restored.ClarificationContext("clarify-1")
	if err != nil {
		t.Fatalf("ClarificationContext() error = %v", err)
	}
	if loadedRequest.Question != request.Question || loadedDispatch.AttemptID != dispatch.AttemptID || len(transcript) != 2 {
		t.Fatalf("clarification context = %#v %#v %#v", loadedRequest, loadedDispatch, transcript)
	}
	updated := append(transcript,
		TaskSessionMessage{Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: request.Question}}},
		TaskSessionMessage{Role: "assistant", Parts: []persistence.MessagePart{{Type: "text", Text: "HTTP 401"}}},
	)
	clarification, err := restored.CompleteClarification("clarify-1", "clarify-message", TaskClarificationResult{
		Answer: []persistence.MessagePart{{Type: "text", Text: "HTTP 401"}}, SessionTranscript: updated,
	})
	if err != nil {
		t.Fatalf("CompleteClarification() error = %v", err)
	}
	if len(clarification.Answer) != 1 || clarification.Answer[0].Text != "HTTP 401" {
		t.Fatalf("clarification = %#v", clarification)
	}
	_, eventsBeforeReplay, err := RestoreDurableHarness(runtimeStore, "run-clarify")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() before replay error = %v", err)
	}
	replayed, err := restored.CompleteClarification("clarify-1", "clarify-message", TaskClarificationResult{Answer: []persistence.MessagePart{{Type: "text", Text: "different"}}})
	if err != nil || !reflect.DeepEqual(replayed, clarification) {
		t.Fatalf("idempotent CompleteClarification() = %#v, %v", replayed, err)
	}
	_, eventsAfterReplay, err := RestoreDurableHarness(runtimeStore, "run-clarify")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() after replay error = %v", err)
	}
	if len(eventsAfterReplay) != len(eventsBeforeReplay) {
		t.Fatalf("completion replay appended events: before=%d after=%d", len(eventsBeforeReplay), len(eventsAfterReplay))
	}
	accepted, ok := restored.TaskResult("classify")
	if !ok || len(accepted.SessionTranscript) != 4 {
		t.Fatalf("accepted result transcript = %#v, %v", accepted.SessionTranscript, ok)
	}
	inspection, err := restored.InspectTask(InspectTaskRequest{RequestID: "inspect-after", TaskName: "classify", Revision: restored.Snapshot().Revision})
	if err != nil {
		t.Fatalf("InspectTask() error = %v", err)
	}
	encodedInspection, _ := json.Marshal(inspection)
	if strings.Contains(string(encodedInspection), "classify request") || strings.Contains(string(encodedInspection), "HTTP 401") {
		t.Fatalf("inspection leaked worker transcript: %s", encodedInspection)
	}
}

func TestDurableTaskProgressRestoresActiveLogicalSessionIdempotently(t *testing.T) {
	t.Parallel()
	runtimeStore := persistence.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-progress", "commander-progress", []TaskDefinition{{Name: "work"}})
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
	progress := TaskAttemptProgress{
		AttemptID: dispatch.AttemptID, Phase: "tool_calls_received",
		Transcript: []TaskSessionMessage{{Role: "assistant", Parts: []persistence.MessagePart{{Type: "tool_use", ToolUseID: "call-1", ToolName: "search", ToolInputJSON: `{}`}}}},
		Tools:      map[string]ToolOperation{"call-1": {ToolUseID: "call-1", ToolName: "search", InputJSON: `{}`, Status: ToolOperationIntended}},
	}
	if err := harness.SaveTaskProgress(dispatch, progress); err != nil {
		t.Fatalf("SaveTaskProgress() error = %v", err)
	}
	restored, eventsBeforeReplay, err := RestoreDurableHarness(runtimeStore, "run-progress")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	got, ok := restored.TaskProgress(dispatch.AttemptID)
	if !ok || !reflect.DeepEqual(got, progress) {
		t.Fatalf("restored progress = %#v, %v; want %#v", got, ok, progress)
	}
	if active := restored.Snapshot().Active; len(active) != 1 || active[0].AttemptID != dispatch.AttemptID {
		t.Fatalf("restored active attempts = %#v", active)
	}
	if err := restored.SaveTaskProgress(dispatch, progress); err != nil {
		t.Fatalf("idempotent SaveTaskProgress() error = %v", err)
	}
	_, eventsAfterReplay, err := RestoreDurableHarness(runtimeStore, "run-progress")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() after replay error = %v", err)
	}
	if len(eventsAfterReplay) != len(eventsBeforeReplay) {
		t.Fatalf("progress replay appended events: before=%d after=%d", len(eventsBeforeReplay), len(eventsAfterReplay))
	}
}

func TestDurableRecoveryBlockWaitsAndResumesOriginalAttempt(t *testing.T) {
	t.Parallel()
	runtimeStore := persistence.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-reconcile", "commander-reconcile", []TaskDefinition{{Name: "work"}})
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
	progress := TaskAttemptProgress{AttemptID: dispatch.AttemptID, Phase: "tool_calls_received", Tools: map[string]ToolOperation{
		"call-1": {ToolUseID: "call-1", ToolName: "charge", InputJSON: `{}`, Status: ToolOperationInFlight},
	}}
	if err := harness.SaveTaskProgress(dispatch, progress); err != nil {
		t.Fatalf("SaveTaskProgress() error = %v", err)
	}
	block := RecoveryBlock{AttemptID: dispatch.AttemptID, TaskName: "work", ToolUseID: "call-1", ToolName: "charge", IdempotencyKey: "key-1", Reason: "outcome unknown"}
	if err := harness.BlockTaskRecovery(dispatch, block); err != nil {
		t.Fatalf("BlockTaskRecovery() error = %v", err)
	}
	if err := harness.EnterRecoveryWaiting(); err != nil {
		t.Fatalf("EnterRecoveryWaiting() error = %v", err)
	}

	restored, events, err := RestoreDurableHarness(runtimeStore, "run-reconcile")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	snapshot = restored.Snapshot()
	if snapshot.RunState != RunWaiting || snapshot.Tasks["work"].State != TaskWaiting || len(snapshot.Active) != 0 || !reflect.DeepEqual(snapshot.RecoveryBlocks[dispatch.AttemptID], block) {
		t.Fatalf("waiting snapshot = %#v", snapshot)
	}
	if err := restored.ResolveRecoveryBlock(dispatch.AttemptID, "verified original result", false); err != nil {
		t.Fatalf("ResolveRecoveryBlock() error = %v", err)
	}
	snapshot = restored.Snapshot()
	if snapshot.RunState != RunRunning || snapshot.Tasks["work"].State != TaskRunning || len(snapshot.Active) != 1 || snapshot.Active[0].AttemptID != dispatch.AttemptID {
		t.Fatalf("resumed snapshot = %#v", snapshot)
	}
	resolved, _ := restored.TaskProgress(dispatch.AttemptID)
	if operation := resolved.Tools["call-1"]; operation.Status != ToolOperationCompleted || operation.Result != "verified original result" {
		t.Fatalf("resolved operation = %#v", operation)
	}
	var blocked, waiting bool
	for _, event := range events {
		blocked = blocked || event.Type == EventTaskRecoveryBlocked
		waiting = waiting || event.Type == EventMissionRecoveryWaiting
	}
	if !blocked || !waiting {
		t.Fatalf("recovery events missing: %#v", events)
	}
}

func TestDurableHumanInputWaitReleasesLeaseAndResumesSameAttempt(t *testing.T) {
	runtimeStore := persistence.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-human-wait", "commander-human-wait", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	dispatch, err := harness.DispatchTask(DispatchRequest{
		RequestID: "dispatch-work", TaskName: "work", ReadySetRevision: harness.Snapshot().Revision, Grant: testGrant("work"),
	})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := harness.AcquireExecutionLease(dispatch.AttemptID, "worker", "lease-token", startedAt, time.Minute); err != nil {
		t.Fatal(err)
	}
	progress := TaskAttemptProgress{AttemptID: dispatch.AttemptID, Phase: "tool_calls_received", Tools: map[string]ToolOperation{
		"tool-use-1": {ToolUseID: "tool-use-1", ToolName: "builtins.human.ask", InputJSON: `{"question":"Proceed?"}`, Status: ToolOperationInFlight, StartedAt: startedAt},
	}}
	if err := harness.SaveTaskProgressWithLease(dispatch, progress, "lease-token"); err != nil {
		t.Fatal(err)
	}
	deadline := startedAt.Add(time.Hour)
	wait := HumanInputWait{
		AttemptID: dispatch.AttemptID, TaskName: "work", ToolUseID: "tool-use-1", ToolName: "builtins.human.ask",
		ToolCallID: "stable-human-call", Question: "Proceed?", Choices: []string{"yes", "no"},
		RequestedAt: startedAt, DeadlineAt: &deadline,
	}
	if err := harness.WaitTaskForHumanInputWithLease(dispatch, wait, "lease-token"); err != nil {
		t.Fatal(err)
	}
	if err := harness.EnterRecoveryWaiting(); err != nil {
		t.Fatal(err)
	}
	restored, events, err := RestoreDurableHarness(runtimeStore, "run-human-wait")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := restored.Snapshot()
	if snapshot.RunState != RunWaiting || snapshot.Tasks["work"].State != TaskWaiting || len(snapshot.Active) != 0 {
		t.Fatalf("waiting snapshot = %#v", snapshot)
	}
	if !reflect.DeepEqual(snapshot.HumanInputWaits[dispatch.AttemptID], wait) {
		t.Fatalf("restored wait = %#v", snapshot.HumanInputWaits[dispatch.AttemptID])
	}
	snapshot.HumanInputWaits[dispatch.AttemptID].Choices[0] = "tampered"
	if restored.Snapshot().HumanInputWaits[dispatch.AttemptID].Choices[0] != "yes" {
		t.Fatal("snapshot mutated durable human input choices")
	}
	if err := restored.SaveTaskProgressWithLease(dispatch, progress, "lease-token"); err == nil {
		t.Fatal("released lease still allowed a stale worker write")
	}
	if err := restored.Stop(); err != nil {
		t.Fatalf("stop waiting run: %v", err)
	}
	if restored.Snapshot().RunState != RunStopped {
		t.Fatal("waiting run did not stop durably")
	}
	if err := restored.Resume(); err != nil {
		t.Fatalf("resume stopped human-input run: %v", err)
	}
	answeredAt := startedAt.Add(time.Minute)
	if err := restored.ResolveHumanInputWait(dispatch.AttemptID, wait.ToolCallID, "yes", "user-1", false, answeredAt); err != nil {
		t.Fatal(err)
	}
	if err := restored.ResolveHumanInputWait(dispatch.AttemptID, wait.ToolCallID, "yes", "user-1", false, answeredAt); err != nil {
		t.Fatalf("same answer was not idempotent: %v", err)
	}
	if err := restored.ResolveHumanInputWait(dispatch.AttemptID, wait.ToolCallID, "no", "user-2", false, answeredAt); err == nil {
		t.Fatal("conflicting second answer was accepted")
	}
	resumed, _, err := RestoreDurableHarness(runtimeStore, "run-human-wait")
	if err != nil {
		t.Fatal(err)
	}
	snapshot = resumed.Snapshot()
	if snapshot.RunState != RunRunning || snapshot.Tasks["work"].State != TaskRunning || len(snapshot.Active) != 1 || snapshot.Active[0].AttemptID != dispatch.AttemptID {
		t.Fatalf("resumed snapshot = %#v", snapshot)
	}
	resolved, _ := resumed.TaskProgress(dispatch.AttemptID)
	if operation := resolved.Tools[wait.ToolUseID]; operation.Status != ToolOperationCompleted || operation.Result != "yes" {
		t.Fatalf("resolved tool operation = %#v", operation)
	}
	var requested, waiting bool
	for _, event := range events {
		requested = requested || event.Type == EventHumanInputRequested
		waiting = waiting || event.Type == EventMissionHumanInputWaiting
	}
	if !requested || !waiting {
		t.Fatalf("human input events missing: %#v", events)
	}
}

func TestCommanderMessagePersistenceFailureDoesNotChangeTranscript(t *testing.T) {
	t.Parallel()

	store := &failingStore{MemoryRuntimeStore: persistence.NewMemoryRuntimeStore()}
	harness, err := CreateDurableHarness(store, "run-1", "commander-1", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	store.failCommit = true
	err = harness.AppendCommanderMessage(CommanderMessage{
		ID:    "message-1",
		Role:  "assistant",
		Parts: []persistence.MessagePart{{Type: "text", Text: "uncommitted"}},
	})
	if err == nil {
		t.Fatal("AppendCommanderMessage() succeeded despite persistence failure")
	}
	if messages := harness.CommanderMessages(); len(messages) != 0 {
		t.Fatalf("uncommitted messages became visible: %#v", messages)
	}
}

func TestPersistenceFailureDoesNotChangeVisibleState(t *testing.T) {
	t.Parallel()

	store := &failingStore{MemoryRuntimeStore: persistence.NewMemoryRuntimeStore()}
	harness, err := CreateDurableHarness(store, "run-1", "commander-1", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	before := harness.Snapshot()
	store.failCommit = true
	if err := harness.Start(); err == nil {
		t.Fatal("Start() succeeded despite persistence failure")
	}
	after := harness.Snapshot()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("state changed after failed persistence: before=%#v after=%#v", before, after)
	}
}

func TestRestoreRejectsEventSequenceGap(t *testing.T) {
	t.Parallel()

	store := &gappedStore{RuntimeStore: persistence.NewMemoryRuntimeStore()}
	_, err := CreateDurableHarness(store, "run-1", "commander-1", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	store.gap = true
	if _, _, err := RestoreDurableHarness(store, "run-1"); err == nil {
		t.Fatal("RestoreDurableHarness() accepted an event sequence gap")
	}
}

func TestStopRestoreResumePreservesCommanderConversation(t *testing.T) {
	t.Parallel()

	runtimeStore := persistence.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-1", "commander-1", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	message := CommanderMessage{ID: "message-1", Role: "assistant", Parts: []persistence.MessagePart{{Type: "text", Text: "Ready to work."}}}
	if err := harness.AppendCommanderMessage(message); err != nil {
		t.Fatalf("AppendCommanderMessage() error = %v", err)
	}
	if err := harness.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	restored, _, err := RestoreDurableHarness(runtimeStore, "run-1")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	if restored.Snapshot().RunState != RunStopped || restored.Snapshot().Commander.State != CommanderStopped {
		t.Fatalf("restored stopped state = %#v", restored.Snapshot())
	}
	if !reflect.DeepEqual(restored.CommanderMessages(), []CommanderMessage{message}) {
		t.Fatalf("restored messages = %#v", restored.CommanderMessages())
	}
	if err := restored.Resume(); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if snapshot := restored.Snapshot(); snapshot.RunState != RunRunning || snapshot.Commander.ID != "commander-1" || snapshot.Commander.State != CommanderActive {
		t.Fatalf("resumed snapshot = %#v", snapshot)
	}
}

func TestDurableRouteNoneCommitsTranscriptAndCompletionTogether(t *testing.T) {
	t.Parallel()
	runtimeStore := persistence.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-none", "commander-none", []TaskDefinition{
		{Name: "classify", Routes: []RouteDefinition{{Target: "branch", Condition: "matches"}}}, {Name: "branch"},
	})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := harness.Snapshot()
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "dispatch", TaskName: "classify", ReadySetRevision: snapshot.Revision, Grant: testGrant("classify")})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	if err := harness.CompleteTask("classify", dispatch.AttemptID, testResult(dispatch)); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	snapshot = harness.Snapshot()
	prompt := CommanderMessage{ID: "route-prompt", Role: "user", Parts: []persistence.MessagePart{{Type: "text", Text: "route"}}}
	message := CommanderMessage{ID: "route-message", Role: "assistant", Parts: []persistence.MessagePart{{Type: "tool_use", ToolUseID: "route-none", ToolName: "select_route"}}}
	selection, err := harness.ApplyCommanderRoute(prompt, message, RouteRequest{RequestID: "route-none", TaskName: "classify", Target: "none", Revision: snapshot.Revision})
	if err != nil {
		t.Fatalf("ApplyCommanderRoute() error = %v", err)
	}
	if selection.Target != "none" || harness.Snapshot().RunState != RunCompleted {
		t.Fatalf("selection/snapshot = %#v / %#v", selection, harness.Snapshot())
	}
	restored, events, err := RestoreDurableHarness(runtimeStore, "run-none")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	if len(restored.CommanderMessages()) != 3 || restored.Snapshot().RouteSelections["classify"].Target != "none" {
		t.Fatalf("restored route = %#v", restored.Snapshot())
	}
	if events[len(events)-1].Type != EventMissionCompleted {
		t.Fatalf("last event = %#v", events[len(events)-1])
	}
}

type failingStore struct {
	*persistence.MemoryRuntimeStore
	failCommit bool
}

func (s *failingStore) CommitRuntime(runID string, expectedRevision uint64, checkpoint persistence.RuntimeCheckpointRecord, events []persistence.RuntimeEventRecord) error {
	if s.failCommit {
		return errors.New("injected commit failure")
	}
	return s.MemoryRuntimeStore.CommitRuntime(runID, expectedRevision, checkpoint, events)
}

type gappedStore struct {
	persistence.RuntimeStore
	gap bool
}

func (s *gappedStore) LoadRuntime(runID string) (persistence.RuntimeCheckpointRecord, []persistence.RuntimeEventRecord, error) {
	checkpoint, events, err := s.RuntimeStore.LoadRuntime(runID)
	if err == nil && s.gap && len(events) > 0 {
		events[0].Sequence = 2
	}
	return checkpoint, events, err
}
