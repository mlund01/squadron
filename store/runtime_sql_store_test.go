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
