package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteRuntimeStorePersistsAtomicRevisionAndEvents(t *testing.T) {
	t.Parallel()

	bundle, err := NewSQLiteBundle(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("NewSQLiteBundle() error = %v", err)
	}
	t.Cleanup(func() { _ = bundle.Close() })
	if bundle.Runtime == nil {
		t.Fatal("SQLite bundle has no RuntimeStore")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	created := RuntimeCheckpointRecord{RunID: "run-1", Revision: 1, PayloadJSON: `{"revision":1}`, UpdatedAt: now}
	if err := bundle.Runtime.CreateRuntime(created, []RuntimeEventRecord{
		{RunID: "run-1", Revision: 1, EventType: "mission.created", OccurredAt: now, PayloadJSON: `{}`},
	}); err != nil {
		t.Fatalf("CreateRuntime() error = %v", err)
	}

	committed := RuntimeCheckpointRecord{RunID: "run-1", Revision: 2, PayloadJSON: `{"revision":2}`, UpdatedAt: now.Add(time.Second)}
	if err := bundle.Runtime.CommitRuntime("run-1", 1, committed, []RuntimeEventRecord{
		{RunID: "run-1", Revision: 2, EventType: "mission.started", OccurredAt: now.Add(time.Second), PayloadJSON: `{}`},
		{RunID: "run-1", Revision: 2, EventType: "ready_set.published", OccurredAt: now.Add(time.Second), PayloadJSON: `{}`},
	}); err != nil {
		t.Fatalf("CommitRuntime() error = %v", err)
	}
	if err := bundle.Runtime.CommitRuntime("run-1", 1, RuntimeCheckpointRecord{
		RunID: "run-1", Revision: 3, PayloadJSON: `{"revision":3}`, UpdatedAt: now.Add(2 * time.Second),
	}, nil); err == nil {
		t.Fatal("CommitRuntime() accepted stale revision")
	}

	checkpoint, events, err := bundle.Runtime.LoadRuntime("run-1")
	if err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	if checkpoint.Revision != 2 || checkpoint.PayloadJSON != committed.PayloadJSON {
		t.Fatalf("checkpoint = %#v, want revision 2", checkpoint)
	}
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence = %d, want %d", index, event.Sequence, index+1)
		}
	}
}

func TestSQLiteMissionMemoryCommitIsAtomicWithRuntime(t *testing.T) {
	bundle, err := NewSQLiteBundle(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Close() })
	memory := bundle.Runtime.(MissionMemoryRuntimeStore)
	now := time.Now().UTC()
	if err := memory.CreateRuntime(RuntimeCheckpointRecord{RunID: "run-1", Revision: 1, PayloadJSON: `{}`, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	commit := func(expected, next, memoryVersion uint64, value string) error {
		return memory.CommitRuntimeWithMemory("run-1", expected,
			RuntimeCheckpointRecord{RunID: "run-1", Revision: next, PayloadJSON: value, UpdatedAt: now},
			[]RuntimeEventRecord{{RunID: "run-1", Revision: next, EventType: "task.completed", OccurredAt: now}},
			[]MissionMemoryWrite{{MissionName: "research", Key: "findings", ValueJSON: value, ExpectedVersion: memoryVersion}})
	}
	if err := commit(1, 2, 0, `{"count":1}`); err != nil {
		t.Fatal(err)
	}
	if err := commit(2, 3, 0, `{"count":2}`); err == nil {
		t.Fatal("accepted stale memory version")
	}
	checkpoint, events, err := memory.LoadRuntime("run-1")
	if err != nil || checkpoint.Revision != 2 || len(events) != 1 {
		t.Fatalf("failed memory commit changed runtime: checkpoint=%+v events=%d err=%v", checkpoint, len(events), err)
	}
	if err := commit(2, 3, 1, `{"count":2}`); err != nil {
		t.Fatal(err)
	}
	entries, err := memory.LoadMissionMemory("research")
	if err != nil || len(entries) != 1 || entries[0].Version != 2 || entries[0].ValueJSON != `{"count":2}` {
		t.Fatalf("memory entries=%+v err=%v", entries, err)
	}
	if err := commit(2, 4, 2, `{"count":3}`); err == nil {
		t.Fatal("accepted stale runtime revision")
	}
	entries, _ = memory.LoadMissionMemory("research")
	if entries[0].Version != 2 {
		t.Fatalf("stale runtime commit changed memory: %+v", entries)
	}
	if err := memory.CreateRuntime(RuntimeCheckpointRecord{RunID: "run-2", Revision: 1, PayloadJSON: `{}`, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	if err := memory.CommitRuntimeWithMemory("run-2", 1,
		RuntimeCheckpointRecord{RunID: "run-2", Revision: 2, PayloadJSON: `{"done":true}`, UpdatedAt: now},
		nil, []MissionMemoryWrite{{MissionName: "research", Key: "findings", ValueJSON: `{"count":3}`, ExpectedVersion: 1}}); err == nil {
		t.Fatal("second run overwrote memory using a stale version")
	}
	other, _, err := memory.LoadRuntime("run-2")
	if err != nil || other.Revision != 1 {
		t.Fatalf("cross-run conflict advanced checkpoint: %+v err=%v", other, err)
	}
	var auditCount int
	if err := bundle.Runtime.(*SQLiteRuntimeStore).db.QueryRow(`SELECT COUNT(*) FROM mission_memory_writes`).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("memory audit count=%d err=%v, want 2", auditCount, err)
	}
}
