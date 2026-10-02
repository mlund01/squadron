package runtime

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"squadron/store"
)

func TestCommanderMemoryPagesAreBoundedAndVersioned(t *testing.T) {
	t.Parallel()
	value := MemoryValueSnapshot{Key: "summary", Value: json.RawMessage(`"` + strings.Repeat("x", maxCommanderMemoryPageBytes+100) + `"`), Version: 3}
	first, err := pageCommanderMemory(InspectMemoryRequest{RequestID: "read-1", Key: "summary"}, value)
	if err != nil || !first.Exists || first.Version != 3 || len(first.Chunk) > maxCommanderMemoryPageBytes || first.NextOffset == 0 {
		t.Fatalf("first page = %+v, err=%v", first, err)
	}
	version := first.Version
	second, err := pageCommanderMemory(InspectMemoryRequest{RequestID: "read-2", Key: "summary", Offset: first.NextOffset, ExpectedVersion: &version}, value)
	if err != nil || second.NextOffset != 0 || first.Chunk+second.Chunk != string(value.Value) {
		t.Fatalf("second page = %+v, err=%v", second, err)
	}
	newValue := MemoryValueSnapshot{Key: "summary", Value: json.RawMessage(`"updated"`), Version: 4}
	stale, err := pageCommanderMemory(InspectMemoryRequest{RequestID: "read-3", Key: "summary", Offset: first.NextOffset, ExpectedVersion: &version}, newValue)
	if err != nil || !stale.Stale || stale.Exists || stale.Chunk != "" {
		t.Fatalf("changed version leaked a mismatched page: %+v, err=%v", stale, err)
	}
	if _, err := pageCommanderMemory(InspectMemoryRequest{RequestID: "read-4", Key: "summary", Offset: first.NextOffset}, value); err == nil {
		t.Fatal("continuation without expected version was accepted")
	}
	missing, err := pageCommanderMemory(InspectMemoryRequest{RequestID: "read-5", Key: "summary"}, MemoryValueSnapshot{Key: "summary", Version: 4})
	if err != nil || missing.Exists || missing.Version != 4 {
		t.Fatalf("missing value = %+v, err=%v", missing, err)
	}
}

func TestCommanderMemoryReadReplaysSameValueAfterRestart(t *testing.T) {
	t.Parallel()
	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-memory-read", "commander", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	request := InspectMemoryRequest{RequestID: "memory-tool", Key: "format", Revision: harness.Snapshot().Revision}
	prompt := CommanderMessage{ID: "read:prompt", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: "ready"}}}
	message := CommanderMessage{ID: "read", Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: request.RequestID, ToolName: "inspect_mission_memory", ToolInputJSON: `{"key":"format"}`}}}
	inspection, err := harness.ApplyCommanderMemoryInspection(prompt, message, request,
		MemoryValueSnapshot{Key: "format", Value: json.RawMessage(`"bullets"`), Version: 2})
	if err != nil || !inspection.Exists || inspection.Chunk != `"bullets"` {
		t.Fatalf("inspection = %+v, err=%v", inspection, err)
	}
	restored, events, err := RestoreDurableHarness(runtimeStore, "run-memory-read")
	if err != nil {
		t.Fatal(err)
	}
	messages := restored.CommanderMessages()
	if len(messages) != 3 || !strings.Contains(messages[2].Parts[0].Text, "bullets") || !strings.Contains(messages[2].Parts[0].Text, `"version":2`) {
		t.Fatalf("read result not replayed: %#v", messages)
	}
	last := events[len(events)-1]
	if last.Type != EventMissionMemoryInspected || last.MemoryRead == nil || last.MemoryRead.Key != "format" || last.MemoryRead.Version != 2 || strings.Contains(last.Error, "bullets") {
		t.Fatalf("memory audit event = %+v", last)
	}
	if _, err := restored.ApplyCommanderMemoryInspection(prompt, message, request,
		MemoryValueSnapshot{Key: "format", Value: json.RawMessage(`"changed"`), Version: 3}); err == nil {
		t.Fatal("stale read turn replaced the durable result")
	}
}

func TestSQLiteCommanderMemoryReadSurvivesReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "commander-memory.db")
	bundle := openSQLiteBundle(t, dbPath)
	harness, err := CreateDurableHarness(bundle.Runtime, "run-sqlite-memory-read", "commander", []TaskDefinition{{Name: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	request := InspectMemoryRequest{RequestID: "read-1", Key: "summary", Revision: harness.Snapshot().Revision}
	prompt := CommanderMessage{ID: "read-1:prompt", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: "ready"}}}
	message := CommanderMessage{ID: "read-1", Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: "read-1", ToolName: "inspect_mission_memory"}}}
	if _, err := harness.ApplyCommanderMemoryInspection(prompt, message, request,
		MemoryValueSnapshot{Key: "summary", Value: json.RawMessage(`"stored answer"`), Version: 7}); err != nil {
		t.Fatal(err)
	}
	closeSQLiteBundle(t, bundle)

	bundle = openSQLiteBundle(t, dbPath)
	defer closeSQLiteBundle(t, bundle)
	restored, _, err := RestoreDurableHarness(bundle.Runtime, "run-sqlite-memory-read")
	if err != nil {
		t.Fatal(err)
	}
	messages := restored.CommanderMessages()
	if len(messages) != 3 || !strings.Contains(messages[2].Parts[0].Text, "stored answer") || !strings.Contains(messages[2].Parts[0].Text, `"version":7`) {
		t.Fatalf("SQLite lost commander memory read: %#v", messages)
	}
}
