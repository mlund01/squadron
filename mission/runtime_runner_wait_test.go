package mission

import (
	"path/filepath"
	"testing"
	"time"

	missionruntime "squadron/mission/runtime"
	"squadron/store"
)

func TestReconcileHumanInputWaitResumesPersistedAttempt(t *testing.T) {
	bundle, err := store.NewSQLiteBundle(filepath.Join(t.TempDir(), "mission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	harness, err := missionruntime.CreateDurableHarness(bundle.Runtime, "run-human-reconcile", "commander-human", []missionruntime.TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	dispatch, err := harness.DispatchTask(missionruntime.DispatchRequest{
		RequestID: "dispatch-work", TaskName: "work", ReadySetRevision: harness.Snapshot().Revision,
		Grant: missionruntime.ExecutionGrant{TaskName: "work", Model: "model", RunAs: "user:test", ConfigurationVersion: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := harness.AcquireExecutionLease(dispatch.AttemptID, "worker", "lease", startedAt, time.Minute); err != nil {
		t.Fatal(err)
	}
	progress := missionruntime.TaskAttemptProgress{AttemptID: dispatch.AttemptID, Phase: "tool_calls_received", Tools: map[string]missionruntime.ToolOperation{
		"ask-1": {ToolUseID: "ask-1", ToolName: "builtins_human_ask", InputJSON: `{"question":"Proceed?"}`, Status: missionruntime.ToolOperationInFlight, StartedAt: startedAt},
	}}
	if err := harness.SaveTaskProgressWithLease(dispatch, progress, "lease"); err != nil {
		t.Fatal(err)
	}
	deadline := startedAt.Add(time.Minute)
	wait := missionruntime.HumanInputWait{
		AttemptID: dispatch.AttemptID, TaskName: "work", ToolUseID: "ask-1", ToolName: "builtins_human_ask",
		ToolCallID: "stable-ask", Question: "Proceed?", RequestedAt: startedAt, DeadlineAt: &deadline,
	}
	if err := bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
		MissionID: dispatch.RunID, ToolCallID: wait.ToolCallID, Question: wait.Question, DeadlineAt: &deadline,
	}); err != nil {
		t.Fatal(err)
	}
	if err := harness.WaitTaskForHumanInputWithLease(dispatch, wait, "lease"); err != nil {
		t.Fatal(err)
	}
	if err := harness.EnterRecoveryWaiting(); err != nil {
		t.Fatal(err)
	}
	if err := (&Runner{stores: bundle}).reconcileHumanInputWaits(harness); err != nil {
		t.Fatal(err)
	}
	if harness.Snapshot().RunState != missionruntime.RunWaiting {
		t.Fatal("open request resumed the attempt")
	}
	if _, err := bundle.HumanInputs.ResolveRequest(wait.ToolCallID, "yes", "user-1"); err != nil {
		t.Fatal(err)
	}
	restored, _, err := missionruntime.RestoreDurableHarness(bundle.Runtime, dispatch.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Runner{stores: bundle}).reconcileHumanInputWaits(restored); err != nil {
		t.Fatal(err)
	}
	snapshot := restored.Snapshot()
	if snapshot.RunState != missionruntime.RunRunning || snapshot.Tasks["work"].State != missionruntime.TaskRunning || len(snapshot.Active) != 1 || snapshot.Active[0].AttemptID != dispatch.AttemptID {
		t.Fatalf("resumed snapshot = %#v", snapshot)
	}
	actual, _ := restored.TaskProgress(dispatch.AttemptID)
	if actual.Tools["ask-1"].Status != missionruntime.ToolOperationCompleted || actual.Tools["ask-1"].Result != "yes" {
		t.Fatalf("resumed tool = %#v", actual.Tools["ask-1"])
	}
}
