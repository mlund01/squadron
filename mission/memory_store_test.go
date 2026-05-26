package mission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"squadron/aitools"
	"squadron/config"
	"squadron/internal/paths"
)

// withTempHome installs a fresh SquadronHome under a t.TempDir and registers
// cleanup so the next test starts from a clean cache.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	paths.ResetHome()
	if err := paths.SetHome(home); err != nil {
		t.Fatalf("set home: %v", err)
	}
	t.Cleanup(paths.ResetHome)
	return home
}

func TestBuildMemoryStore_NoSlots(t *testing.T) {
	withTempHome(t)
	m := &config.Mission{Name: "m"}
	store, err := buildMemoryStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store != nil {
		t.Fatalf("expected nil store when no slots configured, got %+v", store)
	}
}

func TestBuildMemoryStore_MissionMemory(t *testing.T) {
	home := withTempHome(t)

	m := &config.Mission{
		Name: "m",
		Memory: &config.MissionMemory{
			Description: "persistent",
		},
	}

	store, err := buildMemoryStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	abs, writable, err := store.ResolvePath(aitools.MemorySlotName, ".")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if !writable {
		t.Fatal("mission memory must be writable")
	}
	want := filepath.Join(home, "memories", "mission", "m")
	if abs != want {
		t.Fatalf("mission memory path: want %s, got %s", want, abs)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		t.Fatalf("mission memory directory not created at %s: %v", abs, err)
	}

	// The mission name is NOT a valid slot key — prevents regression.
	if _, _, err := store.ResolvePath(m.Name, "."); err == nil {
		t.Fatal("expected error when resolving by mission name")
	}
}

func TestBuildMemoryStore_Scratchpad_CreatesSidecar(t *testing.T) {
	home := withTempHome(t)
	cleanup := 7
	m := &config.Mission{
		Name: "m",
		Scratchpad: &config.MissionScratchpad{
			Cleanup: &cleanup,
		},
	}

	store, err := buildMemoryStore(m, nil, "mid-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	abs, writable, err := store.ResolvePath(aitools.ScratchpadSlotName, ".")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if !writable {
		t.Fatal("scratchpad must be writable")
	}
	want := filepath.Join(home, "scratchpads", "m", "mid-abc")
	if abs != want {
		t.Fatalf("scratchpad path: want %s, got %s", want, abs)
	}

	metaBytes, err := os.ReadFile(filepath.Join(abs, runMetadataFile))
	if err != nil {
		t.Fatalf("sidecar not written: %v", err)
	}
	var meta runMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("sidecar not valid JSON: %v", err)
	}
	if meta.CleanupDays != 7 {
		t.Fatalf("CleanupDays: want 7, got %d", meta.CleanupDays)
	}
	if meta.MissionID != "mid-abc" {
		t.Fatalf("MissionID: want mid-abc, got %q", meta.MissionID)
	}
	if meta.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set")
	}
}

func TestBuildMemoryStore_Scratchpad_SidecarPreservedOnResume(t *testing.T) {
	home := withTempHome(t)
	m := &config.Mission{
		Name:       "m",
		Scratchpad: &config.MissionScratchpad{},
	}

	// First build: sidecar written
	if _, err := buildMemoryStore(m, nil, "mid-1"); err != nil {
		t.Fatalf("first build: %v", err)
	}
	runDir := filepath.Join(home, "scratchpads", "m", "mid-1")
	firstMetaBytes, _ := os.ReadFile(filepath.Join(runDir, runMetadataFile))
	var first runMetadata
	_ = json.Unmarshal(firstMetaBytes, &first)

	time.Sleep(10 * time.Millisecond)

	// Second build (same missionID = resume): sidecar must NOT be overwritten
	if _, err := buildMemoryStore(m, nil, "mid-1"); err != nil {
		t.Fatalf("second build: %v", err)
	}
	secondMetaBytes, _ := os.ReadFile(filepath.Join(runDir, runMetadataFile))
	var second runMetadata
	_ = json.Unmarshal(secondMetaBytes, &second)

	if !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("CreatedAt should be preserved on resume: first=%v second=%v", first.CreatedAt, second.CreatedAt)
	}
}

func TestBuildMemoryStore_Scratchpad_RequiresMissionID(t *testing.T) {
	withTempHome(t)
	m := &config.Mission{
		Name:       "m",
		Scratchpad: &config.MissionScratchpad{},
	}
	_, err := buildMemoryStore(m, nil, "")
	if err == nil {
		t.Fatal("expected error when missionID is empty")
	}
}

func TestBuildMemoryStore_RejectsReservedSharedMemoryNames(t *testing.T) {
	withTempHome(t)
	for _, reserved := range []string{"memory", "scratchpad"} {
		m := &config.Mission{
			Name:     "m",
			Memories: []string{reserved},
		}
		mems := []config.Memory{{Name: reserved}}
		_, err := buildMemoryStore(m, mems, "mid-1")
		if err == nil {
			t.Fatalf("expected error for reserved shared memory name %q", reserved)
		}
	}
}

func TestBuildMemoryStore_BothMemoryAndScratchpad(t *testing.T) {
	withTempHome(t)
	m := &config.Mission{
		Name:       "m",
		Memory:     &config.MissionMemory{},
		Scratchpad: &config.MissionScratchpad{},
	}
	store, err := buildMemoryStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err := store.ResolvePath(aitools.MemorySlotName, "."); err != nil {
		t.Fatalf("memory slot should resolve: %v", err)
	}
	if _, _, err := store.ResolvePath(aitools.ScratchpadSlotName, "."); err != nil {
		t.Fatalf("scratchpad slot should resolve: %v", err)
	}
}

func TestBuildMemoryStore_SharedMemory(t *testing.T) {
	home := withTempHome(t)
	m := &config.Mission{
		Name:     "m",
		Memories: []string{"research"},
	}
	mems := []config.Memory{
		{Name: "research", Description: "Research notes", Editable: true},
	}

	store, err := buildMemoryStore(m, mems, "mid-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	abs, writable, err := store.ResolvePath("research", ".")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !writable {
		t.Fatal("editable shared memory must resolve as writable")
	}
	want := filepath.Join(home, "memories", "shared", "research")
	if abs != want {
		t.Fatalf("shared memory path: want %s, got %s", want, abs)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		t.Fatalf("shared memory directory not created: %v", err)
	}
}

func TestBuildMemoryStore_SharedMemory_ReadOnly(t *testing.T) {
	withTempHome(t)
	m := &config.Mission{
		Name:     "m",
		Memories: []string{"reference"},
	}
	mems := []config.Memory{
		{Name: "reference"}, // editable defaults to false
	}

	store, err := buildMemoryStore(m, mems, "mid-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	_, writable, err := store.ResolvePath("reference", ".")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if writable {
		t.Fatal("default shared memory must be read-only")
	}
}

func TestResolvePath_EmptySlotNameRejected(t *testing.T) {
	withTempHome(t)
	m := &config.Mission{
		Name:   "m",
		Memory: &config.MissionMemory{},
	}
	store, err := buildMemoryStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, err := store.ResolvePath("", "."); err == nil {
		t.Fatal("expected error when slot name is empty")
	}
}

func TestResolvePath_RejectsPathEscape(t *testing.T) {
	withTempHome(t)
	m := &config.Mission{
		Name:   "m",
		Memory: &config.MissionMemory{},
	}
	store, err := buildMemoryStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, err := store.ResolvePath("memory", "../outside"); err == nil {
		t.Fatal("expected path-escape error")
	}
}

func TestResolvePath_UnknownSlot(t *testing.T) {
	withTempHome(t)
	m := &config.Mission{
		Name:   "m",
		Memory: &config.MissionMemory{},
	}
	store, err := buildMemoryStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, err := store.ResolvePath("does_not_exist", "."); err == nil {
		t.Fatal("expected error for unknown slot")
	}
}

// --- SweepExpiredScratchpads ----------------------------------------------

// writeScratchpad builds a fake per-run scratchpad directory under
// <home>/scratchpads/<mission>/<run-id>, with a sidecar recording the given
// created_at and cleanup_days.
func writeScratchpad(t *testing.T, home, missionName, runID string, createdAt time.Time, cleanupDays int) string {
	t.Helper()
	dir := filepath.Join(home, "scratchpads", missionName, runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	meta := runMetadata{
		Mission:     missionName,
		MissionID:   runID,
		CreatedAt:   createdAt,
		CleanupDays: cleanupDays,
	}
	b, _ := json.Marshal(&meta)
	if err := os.WriteFile(filepath.Join(dir, runMetadataFile), b, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSweep_DeletesExpired(t *testing.T) {
	home := withTempHome(t)
	expired := writeScratchpad(t, home, "m", "old", time.Now().Add(-8*24*time.Hour), 7)

	removed, err := SweepExpiredScratchpads()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected 1 removal, got %v", removed)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatalf("expired directory should be gone: %v", err)
	}
}

func TestSweep_KeepsUnexpired(t *testing.T) {
	home := withTempHome(t)
	fresh := writeScratchpad(t, home, "m", "new", time.Now().Add(-2*24*time.Hour), 7)

	removed, err := SweepExpiredScratchpads()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("expected nothing removed, got %v", removed)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh directory should still exist: %v", err)
	}
}

func TestSweep_IgnoresZeroCleanup(t *testing.T) {
	home := withTempHome(t)
	keep := writeScratchpad(t, home, "m", "forever", time.Now().Add(-365*24*time.Hour), 0)

	removed, err := SweepExpiredScratchpads()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("expected nothing removed when cleanup=0, got %v", removed)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("directory with cleanup=0 should be preserved: %v", err)
	}
}

func TestSweep_IgnoresDirectoriesWithoutSidecar(t *testing.T) {
	home := withTempHome(t)
	manual := filepath.Join(home, "scratchpads", "m", "hand_made")
	if err := os.MkdirAll(manual, 0755); err != nil {
		t.Fatal(err)
	}

	removed, err := SweepExpiredScratchpads()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("sweep must leave un-marked directories alone, got %v", removed)
	}
	if _, err := os.Stat(manual); err != nil {
		t.Fatalf("manually created directory should still exist: %v", err)
	}
}

func TestSweep_MissingRootIsNotAnError(t *testing.T) {
	withTempHome(t) // home is set, but scratchpads/ subtree doesn't exist yet
	removed, err := SweepExpiredScratchpads()
	if err != nil {
		t.Fatalf("sweep should tolerate missing root: %v", err)
	}
	if removed != nil {
		t.Fatalf("expected nil removed, got %v", removed)
	}
}

func TestSweep_WalksAcrossMissions(t *testing.T) {
	home := withTempHome(t)
	a := writeScratchpad(t, home, "alpha", "run1", time.Now().Add(-10*24*time.Hour), 2)
	b := writeScratchpad(t, home, "beta", "run1", time.Now().Add(-10*24*time.Hour), 2)
	keep := writeScratchpad(t, home, "alpha", "run2", time.Now().Add(-1*24*time.Hour), 2)

	removed, err := SweepExpiredScratchpads()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removals across missions, got %v", removed)
	}
	for _, gone := range []string{a, b} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("expected %s gone: %v", gone, err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unexpired directory should remain: %v", err)
	}
}

// TestSweepThenRebuildRoundTrip mirrors the real flow: an old scratchpad
// exists with a sidecar backdated past its cleanup window, the sweep deletes
// it, then a new buildMemoryStore (different missionID) creates a fresh
// scratchpad with a current sidecar — and the old one is gone.
func TestSweepThenRebuildRoundTrip(t *testing.T) {
	home := withTempHome(t)
	stale := writeScratchpad(t, home, "demo", "old-run", time.Now().Add(-5*24*time.Hour), 2)

	if _, err := SweepExpiredScratchpads(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale scratchpad should have been deleted: %v", err)
	}

	cleanup := 2
	m := &config.Mission{
		Name: "demo",
		Scratchpad: &config.MissionScratchpad{
			Cleanup: &cleanup,
		},
	}
	store, err := buildMemoryStore(m, nil, "new-run")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	fresh, _, err := store.ResolvePath(aitools.ScratchpadSlotName, ".")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(home, "scratchpads", "demo", "new-run")
	if fresh != want {
		t.Fatalf("fresh path: want %s, got %s", want, fresh)
	}

	metaBytes, err := os.ReadFile(filepath.Join(fresh, runMetadataFile))
	if err != nil {
		t.Fatalf("fresh sidecar: %v", err)
	}
	var meta runMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	if time.Since(meta.CreatedAt) > time.Minute {
		t.Fatalf("fresh sidecar CreatedAt should be ~now, got %v", meta.CreatedAt)
	}
}

func TestWriteRunMetadata_PreservesOnReentry(t *testing.T) {
	// Direct test of the O_CREATE|O_EXCL sidecar write: calling twice with
	// different cleanupDays must not overwrite the original.
	dir := t.TempDir()
	if err := writeRunMetadata(dir, "m", "id-1", 7); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, runMetadataFile))

	time.Sleep(10 * time.Millisecond)

	if err := writeRunMetadata(dir, "m", "id-1", 99); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, runMetadataFile))

	if string(first) != string(second) {
		t.Fatalf("sidecar must not be rewritten:\nfirst:  %s\nsecond: %s", first, second)
	}
}
