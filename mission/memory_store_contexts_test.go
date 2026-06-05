package mission

import (
	"os"
	"path/filepath"
	"testing"

	"squadron/aitools"
	"squadron/config"
)

func TestBuildMemoryStore_Contexts_MissionLevel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	contexts := []config.Context{{Name: "research", Path: dir, Description: "research docs"}}
	m := &config.Mission{Name: "m", Contexts: []string{"research"}}

	store, err := buildMemoryStore(m, nil, contexts, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	abs, err := store.ResolvePath(aitools.ContextSlotPrefix+"research", ".")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if abs != dir {
		t.Fatalf("expected %s, got %s", dir, abs)
	}

	// The bare name without prefix must NOT resolve — contexts live in a
	// distinct slot namespace.
	if _, err := store.ResolvePath("research", "."); err == nil {
		t.Fatal("expected error when resolving context by bare name")
	}
}

func TestBuildMemoryStore_Contexts_TaskLevel(t *testing.T) {
	dir := t.TempDir()
	contexts := []config.Context{{Name: "kb", Path: dir}}
	m := &config.Mission{
		Name: "m",
		Tasks: []config.Task{
			{Name: "t1", Contexts: []string{"kb"}},
		},
	}

	store, err := buildMemoryStore(m, nil, contexts, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if _, err := store.ResolvePath(aitools.ContextSlotPrefix+"kb", "."); err != nil {
		t.Fatalf("task-level context not registered: %v", err)
	}
}

func TestBuildMemoryStore_Contexts_NotFound(t *testing.T) {
	m := &config.Mission{Name: "m", Contexts: []string{"missing"}}
	if _, err := buildMemoryStore(m, nil, nil, "mid-1"); err == nil {
		t.Fatal("expected error for unknown context reference")
	}
}

func TestBuildMemoryStore_MemoryInfos_IncludesContext(t *testing.T) {
	dir := t.TempDir()
	contexts := []config.Context{{Name: "ref", Path: dir, Description: "reference"}}
	m := &config.Mission{Name: "m", Contexts: []string{"ref"}}
	store, err := buildMemoryStore(m, nil, contexts, "mid-1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, info := range store.MemoryInfos() {
		if info.Name == aitools.ContextSlotPrefix+"ref" {
			found = true
			if info.Description != "reference" {
				t.Fatalf("description = %q", info.Description)
			}
		}
	}
	if !found {
		t.Fatal("context not present in MemoryInfos")
	}
}
