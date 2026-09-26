package wsbridge

import (
	"encoding/json"
	"testing"
	"time"

	missionruntime "squadron/mission/runtime"
	"squadron/store"
)

func TestMissionRuntimeProjectionUsesDurableCheckpoint(t *testing.T) {
	runtimeStore := store.NewMemoryRuntimeStore()
	when := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	checkpoint := missionruntime.Checkpoint{
		RunID: "run-1", Revision: 2, RunState: missionruntime.RunRunning,
		Commander:         missionruntime.CommanderSession{ID: "run-1:commander", State: missionruntime.CommanderActive},
		CommanderMessages: []missionruntime.CommanderMessage{{ID: "message-1", Role: "assistant", Parts: []store.MessagePart{{Type: "text", Text: "Starting research."}}}},
		TaskStates:        map[string]missionruntime.TaskState{"research": missionruntime.TaskRunning},
		Attempts:          map[string]int{"research": 1},
		Dispatches: map[string]missionruntime.Dispatch{"attempt-1": {
			RunID: "run-1", AttemptID: "attempt-1", TaskName: "research", Attempt: 1, DispatchedAt: when,
			Grant: missionruntime.ExecutionGrant{Model: "models.fast", Tools: []string{"builtins.http.get"}, RunAs: "user:local"},
		}},
		TaskProgress: map[string]missionruntime.TaskAttemptProgress{"attempt-1": {
			AttemptID: "attempt-1", Phase: "waiting_for_tool",
			Transcript: []missionruntime.TaskSessionMessage{{Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolName: "builtins.http.get", ToolUseID: "tool-1"}}}},
			Tools:      map[string]missionruntime.ToolOperation{"tool-1": {ToolUseID: "tool-1", ToolName: "builtins.http.get", Status: missionruntime.ToolOperationInFlight}},
		}},
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimeStore.CreateRuntime(store.RuntimeCheckpointRecord{RunID: "run-1", Revision: 2, PayloadJSON: string(payload)}, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err := missionRuntimeProjection(runtimeStore, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.State != "running" || got.Commander.Messages[0].Parts[0].Text != "Starting research." {
		t.Fatalf("commander projection = %#v", got)
	}
	if len(got.Tasks) != 1 || len(got.Tasks[0].Attempts) != 1 {
		t.Fatalf("task projection = %#v", got.Tasks)
	}
	attempt := got.Tasks[0].Attempts[0]
	if attempt.Model != "models.fast" || attempt.Phase != "waiting_for_tool" || attempt.Messages[0].Parts[0].ToolUseID != "tool-1" || attempt.ToolOperations[0].Status != "in_flight" {
		t.Fatalf("attempt projection = %#v", attempt)
	}
}

func TestMissionRuntimeProjectionFallsBackForOlderRun(t *testing.T) {
	got, events, err := missionRuntimeProjection(store.NewMemoryRuntimeStore(), "older-run")
	if err != nil || got != nil || events != nil {
		t.Fatalf("older run projection = %#v, %#v, %v", got, events, err)
	}
}

func TestMissionRuntimeProjectionUsesAcceptedTranscriptAfterCompletion(t *testing.T) {
	runtimeStore := store.NewMemoryRuntimeStore()
	checkpoint := missionruntime.Checkpoint{
		RunID: "finished-run", Revision: 3, RunState: missionruntime.RunCompleted,
		TaskStates: map[string]missionruntime.TaskState{"research": missionruntime.TaskCompleted},
		Dispatches: map[string]missionruntime.Dispatch{"attempt-1": {AttemptID: "attempt-1", TaskName: "research", Attempt: 1}},
		Results: map[string]missionruntime.AcceptedTaskResult{"research": {
			Envelope: missionruntime.TaskResultEnvelope{Summary: "Completed research", Provenance: missionruntime.ResultProvenance{AttemptID: "attempt-1"}},
			SessionTranscript: []missionruntime.TaskSessionMessage{{Role: "assistant", Parts: []store.MessagePart{{Type: "text", Text: "The answer."}}}},
		}},
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimeStore.CreateRuntime(store.RuntimeCheckpointRecord{RunID: "finished-run", Revision: 3, PayloadJSON: string(payload)}, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err := missionRuntimeProjection(runtimeStore, "finished-run")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tasks[0].Summary != "Completed research" || got.Tasks[0].Attempts[0].Messages[0].Parts[0].Text != "The answer." {
		t.Fatalf("completed task projection = %#v", got.Tasks[0])
	}
}
