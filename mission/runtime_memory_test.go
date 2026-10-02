package mission

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"squadron/config"
	missionruntime "squadron/mission/runtime"
)

func TestRuntimeAttemptScratchpadsAreStableAndIsolated(t *testing.T) {
	withTempHome(t)
	runner := &Runner{cfg: &config.Config{}, mission: &config.Mission{
		Name: "research", Scratchpad: true, Tasks: []config.Task{{Name: "collect"}, {Name: "summarize"}},
	}}
	first := missionruntime.Dispatch{RunID: "run-1", TaskName: "collect", AttemptID: "run-1:collect:1"}
	tools, err := runner.runtimeAttemptTools(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	created := tools["file_create"].Call(context.Background(), `{"slot":"scratchpad","path":"notes.txt","content":"private draft"}`)
	if strings.HasPrefix(created, "Error:") {
		t.Fatalf("create scratchpad file: %s", created)
	}
	restarted, err := runner.runtimeAttemptTools(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	read := restarted["file_read"].Call(context.Background(), `{"slot":"scratchpad","path":"notes.txt"}`)
	if !strings.Contains(read, "private draft") {
		t.Fatalf("same attempt did not restore its scratchpad: %s", read)
	}
	for _, other := range []missionruntime.Dispatch{
		{RunID: "run-1", TaskName: "collect", AttemptID: "run-1:collect:2"},
		{RunID: "run-1", TaskName: "summarize", AttemptID: "run-1:summarize:1"},
	} {
		isolated, err := runner.runtimeAttemptTools(context.Background(), other)
		if err != nil {
			t.Fatal(err)
		}
		read = isolated["file_read"].Call(context.Background(), `{"slot":"scratchpad","path":"notes.txt"}`)
		if strings.Contains(read, "private draft") {
			t.Fatalf("attempt %q read another attempt's draft: %s", other.AttemptID, read)
		}
	}
}

func TestRuntimePacketSlotsDoNotLeakAcrossTasks(t *testing.T) {
	packetRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(packetRoot, "source.txt"), []byte("reference data"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{cfg: &config.Config{Packets: []config.Packet{{Name: "sources", Path: packetRoot}}}, mission: &config.Mission{
		Name: "research", Tasks: []config.Task{{Name: "collect", Packets: []string{"sources"}}, {Name: "summarize"}},
	}}
	collect, err := runner.runtimeAttemptTools(context.Background(), missionruntime.Dispatch{RunID: "run-1", TaskName: "collect", AttemptID: "collect-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, writable := collect["file_create"]; writable {
		t.Fatal("packet-only task received a write tool")
	}
	read := collect["file_read"].Call(context.Background(), `{"slot":"packet.sources","path":"source.txt"}`)
	if !strings.Contains(read, "reference data") {
		t.Fatalf("declared packet was unavailable: %s", read)
	}
	summarize, err := runner.runtimeAttemptTools(context.Background(), missionruntime.Dispatch{RunID: "run-1", TaskName: "summarize", AttemptID: "summarize-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(summarize) != 0 {
		t.Fatalf("sibling packet leaked into task: %v", summarize)
	}
}

func TestRuntimeRejectsSharedMemoryWithoutTaskScopedPolicy(t *testing.T) {
	runner := &Runner{mission: &config.Mission{Name: "research", Memories: []string{"shared"}}}
	if err := runner.runMissionScoped(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "shared memory") {
		t.Fatalf("shared memory was silently ignored: %v", err)
	}
}
