package mission

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepExpiredMissionRecords_Disabled(t *testing.T) {
	purged, err := SweepExpiredMissionRecords(nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged = %d, want 0", purged)
	}
	purged, err = SweepExpiredMissionRecords(nil, -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged = %d, want 0", purged)
	}
}

func TestSweepExpiredDebugDirs(t *testing.T) {
	root := t.TempDir()
	oldName := "demo_" + time.Now().Add(-10*24*time.Hour).Format(debugDirTimeLayout)
	freshName := "demo_" + time.Now().Add(-2*24*time.Hour).Format(debugDirTimeLayout)
	chatOld := "chat_agent_" + time.Now().Add(-40*24*time.Hour).Format(debugDirTimeLayout)
	unparseable := "notes"

	for _, name := range []string{oldName, freshName, chatOld, unparseable} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "not-a-dir"), []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	removed, err := SweepExpiredDebugDirs(7, []string{root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantRemoved := map[string]bool{
		filepath.Join(root, oldName): true,
		filepath.Join(root, chatOld): true,
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d dirs, want 2: %v", len(removed), removed)
	}
	for _, p := range removed {
		if !wantRemoved[p] {
			t.Fatalf("unexpected removal: %s", p)
		}
	}

	if _, err := os.Stat(filepath.Join(root, freshName)); err != nil {
		t.Fatalf("fresh dir should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, unparseable)); err != nil {
		t.Fatalf("unparseable dir should remain: %v", err)
	}
}

func TestSweepExpiredDebugDirs_Disabled(t *testing.T) {
	root := t.TempDir()
	name := "demo_" + time.Now().Add(-40*24*time.Hour).Format(debugDirTimeLayout)
	if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
		t.Fatal(err)
	}
	removed, err := SweepExpiredDebugDirs(0, []string{root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v, want none", removed)
	}
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("dir should remain when ttl is 0: %v", err)
	}
}

func TestDebugSweepRoots_DedupesCWD(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	roots := DebugSweepRoots(cwd)
	seen := map[string]int{}
	for _, r := range roots {
		seen[r]++
		if seen[r] > 1 {
			t.Fatalf("duplicate root %s in %v", r, roots)
		}
	}
	want := filepath.Join(cwd, "debug")
	found := false
	for _, r := range roots {
		if r == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("roots %v missing %s", roots, want)
	}
}

func TestSweepExpiredDebugDirs_MissingRoot(t *testing.T) {
	removed, err := SweepExpiredDebugDirs(7, []string{filepath.Join(t.TempDir(), "nope")})
	if err != nil {
		t.Fatalf("missing root should not error: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v, want none", removed)
	}
}
