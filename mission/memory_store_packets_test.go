package mission

import (
	"os"
	"path/filepath"
	"testing"

	"squadron/aitools"
	"squadron/config"
)

func TestBuildMemoryStore_Packets_MissionLevel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	packets := []config.Packet{{Name: "research", Path: dir, Description: "research docs"}}
	m := &config.Mission{Name: "m", Packets: []string{"research"}}

	store, err := buildMemoryStore(m, nil, packets, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	abs, err := store.ResolvePath(aitools.PacketSlotPrefix+"research", ".")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if abs != dir {
		t.Fatalf("expected %s, got %s", dir, abs)
	}

	// The bare name without prefix must NOT resolve — contexts live in a
	// distinct slot namespace.
	if _, err := store.ResolvePath("research", "."); err == nil {
		t.Fatal("expected error when resolving packet by bare name")
	}
}

func TestBuildMemoryStore_Packets_TaskLevel(t *testing.T) {
	dir := t.TempDir()
	packets := []config.Packet{{Name: "kb", Path: dir}}
	m := &config.Mission{
		Name: "m",
		Tasks: []config.Task{
			{Name: "t1", Packets: []string{"kb"}},
		},
	}

	store, err := buildMemoryStore(m, nil, packets, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if _, err := store.ResolvePath(aitools.PacketSlotPrefix+"kb", "."); err != nil {
		t.Fatalf("task-level packet not registered: %v", err)
	}
}

func TestBuildMemoryStore_Packets_NotFound(t *testing.T) {
	m := &config.Mission{Name: "m", Packets: []string{"missing"}}
	if _, err := buildMemoryStore(m, nil, nil, "mid-1"); err == nil {
		t.Fatal("expected error for unknown packet reference")
	}
}

func TestBuildMemoryStore_MemoryInfos_IncludesPacket(t *testing.T) {
	dir := t.TempDir()
	packets := []config.Packet{{Name: "ref", Path: dir, Description: "reference"}}
	m := &config.Mission{Name: "m", Packets: []string{"ref"}}
	store, err := buildMemoryStore(m, nil, packets, "mid-1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, info := range store.MemoryInfos() {
		if info.Name == aitools.PacketSlotPrefix+"ref" {
			found = true
			if info.Description != "reference" {
				t.Fatalf("description = %q", info.Description)
			}
		}
	}
	if !found {
		t.Fatal("packet not present in MemoryInfos")
	}
}
