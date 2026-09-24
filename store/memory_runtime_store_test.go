package store

import "testing"

func TestMemoryRuntimeStoreRejectsStaleCommit(t *testing.T) {
	t.Parallel()

	runtimeStore := NewMemoryRuntimeStore()
	if err := runtimeStore.CreateRuntime(
		RuntimeCheckpointRecord{RunID: "run-1", Revision: 1, PayloadJSON: `{}`},
		[]RuntimeEventRecord{{RunID: "run-1", Revision: 1, EventType: "mission.created", PayloadJSON: `{}`}},
	); err != nil {
		t.Fatalf("CreateRuntime() error = %v", err)
	}
	if err := runtimeStore.CommitRuntime(
		"run-1",
		0,
		RuntimeCheckpointRecord{RunID: "run-1", Revision: 2, PayloadJSON: `{}`},
		nil,
	); err == nil {
		t.Fatal("CommitRuntime() accepted a stale revision")
	}
	checkpoint, events, err := runtimeStore.LoadRuntime("run-1")
	if err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	if checkpoint.Revision != 1 || len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("stored state changed after rejected commit: checkpoint=%#v events=%#v", checkpoint, events)
	}
}
