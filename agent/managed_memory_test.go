package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"squadron/config"
)

func TestManagedMemoryPoliciesAndIsolation(t *testing.T) {
	root := t.TempDir()
	if store, err := NewManagedMemoryStore(root, nil, "owner", "helper", "session"); err != nil || store != nil {
		t.Fatalf("disabled memory: store=%v err=%v", store, err)
	}
	sessionPolicy := &config.AgentMemoryPolicy{Mode: "session", RetentionDays: 7}
	first, err := NewManagedMemoryStore(root, sessionPolicy, "alice", "helper", "one")
	if err != nil {
		t.Fatal(err)
	}
	firstPath, err := first.ResolvePath("memory", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(firstPath, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ owner, agent, session string }{
		{"alice", "helper", "two"},
		{"bob", "helper", "one"},
		{"alice", "other", "one"},
	} {
		other, err := NewManagedMemoryStore(root, sessionPolicy, tc.owner, tc.agent, tc.session)
		if err != nil {
			t.Fatal(err)
		}
		path, err := other.ResolvePath("memory", "note.txt")
		if err != nil {
			t.Fatal(err)
		}
		if path == firstPath {
			t.Fatalf("memory leaked across %+v", tc)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("another session can read first session memory: %v", err)
		}
	}
	resumed, err := NewManagedMemoryStore(root, sessionPolicy, "alice", "helper", "one")
	if err != nil {
		t.Fatal(err)
	}
	resumedPath, err := resumed.ResolvePath("memory", "note.txt")
	if err != nil || resumedPath != firstPath {
		t.Fatalf("session did not resume: %q %v", resumedPath, err)
	}
	if _, err := first.ResolvePath("other", "note.txt"); err == nil {
		t.Fatal("unexpected access to another slot")
	}
	for _, path := range []string{"../escape", "../../other", "/tmp/escape"} {
		if _, err := first.ResolvePath("memory", path); err == nil {
			t.Fatalf("accepted path escape %q", path)
		}
	}
	if strings.Contains(firstPath, "alice") {
		t.Fatal("owner identifier exposed in memory path")
	}
}

func TestPersistentManagedMemoryExpiresWithinOwnerScope(t *testing.T) {
	root := t.TempDir()
	policy := &config.AgentMemoryPolicy{Mode: "persistent", Scope: "owner", RetentionDays: 30}
	first, err := NewManagedMemoryStore(root, policy, "alice", "helper", "one")
	if err != nil {
		t.Fatal(err)
	}
	path, err := first.ResolvePath("memory", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("shared across own sessions"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := NewManagedMemoryStore(root, policy, "alice", "helper", "two")
	if err != nil {
		t.Fatal(err)
	}
	secondPath, _ := second.ResolvePath("memory", "note.txt")
	if secondPath != path {
		t.Fatal("owner-scoped persistent memory changed across sessions")
	}
	bob, err := NewManagedMemoryStore(root, policy, "bob", "helper", "two")
	if err != nil {
		t.Fatal(err)
	}
	bobPath, _ := bob.ResolvePath("memory", "note.txt")
	if bobPath == path {
		t.Fatal("persistent memory leaked across owners")
	}
	metadataPath := filepath.Join(filepath.Dir(filepath.Dir(path)), "metadata.json")
	old, _ := json.Marshal(managedMemoryMetadata{LastUsedAt: time.Now().Add(-31 * 24 * time.Hour)})
	if err := os.WriteFile(metadataPath, old, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManagedMemoryStore(root, policy, "alice", "helper", "three"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired memory still present: %v", err)
	}
}

func TestSweepExpiredManagedMemory(t *testing.T) {
	root := t.TempDir()
	policy := &config.AgentMemoryPolicy{Mode: "session", RetentionDays: 7}
	oldStore, err := NewManagedMemoryStore(root, policy, "alice", "helper", "old")
	if err != nil {
		t.Fatal(err)
	}
	oldPath, _ := oldStore.ResolvePath("memory", "note.txt")
	if err := os.WriteFile(oldPath, []byte("expired"), 0600); err != nil {
		t.Fatal(err)
	}
	activeStore, err := NewManagedMemoryStore(root, policy, "alice", "helper", "active")
	if err != nil {
		t.Fatal(err)
	}
	activePath, _ := activeStore.ResolvePath("memory", "note.txt")
	if err := os.WriteFile(activePath, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(filepath.Dir(filepath.Dir(oldPath)), "metadata.json")
	old, _ := json.Marshal(managedMemoryMetadata{LastUsedAt: time.Now().Add(-8 * 24 * time.Hour), RetentionDays: 7})
	if err := os.WriteFile(metadataPath, old, 0600); err != nil {
		t.Fatal(err)
	}
	removed, err := SweepExpiredManagedMemory(root)
	if err != nil || len(removed) != 1 {
		t.Fatalf("sweep: removed=%v err=%v", removed, err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expired scope retained: %v", err)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active scope removed: %v", err)
	}
}
