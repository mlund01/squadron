package mission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"squadron/aitools"
	"squadron/config"
)

func TestBuildFolderStore_NoFolders(t *testing.T) {
	m := &config.Mission{Name: "m"}
	store, err := buildFolderStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store != nil {
		t.Fatalf("expected nil store when no folders configured, got %+v", store)
	}
}

func TestBuildFolderStore_MissionFolder(t *testing.T) {
	dir := t.TempDir()
	missionDir := filepath.Join(dir, "persistent")

	m := &config.Mission{
		Name: "m",
		Folder: &config.MissionFolder{
			Path:        missionDir,
			Description: "persistent",
		},
	}

	store, err := buildFolderStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	// Registered under reserved name "mission", not the mission name
	abs, writable, err := store.ResolvePath(aitools.MissionFolderName, ".")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if !writable {
		t.Fatal("mission folder must be writable")
	}
	if !filepath.IsAbs(abs) {
		t.Fatalf("resolved path should be absolute: %s", abs)
	}
	// Directory was created
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		t.Fatalf("mission folder not created at %s: %v", abs, err)
	}

	// The mission name is NOT a valid folder key — prevents regression
	if _, _, err := store.ResolvePath(m.Name, "."); err == nil {
		t.Fatal("expected error when resolving by mission name")
	}
}

func TestBuildFolderStore_RunFolder_CreatesSidecar(t *testing.T) {
	dir := t.TempDir()
	m := &config.Mission{
		Name: "m",
		RunFolder: &config.MissionRunFolder{
			Base:    filepath.Join(dir, "runs"),
			Cleanup: 7,
		},
	}

	store, err := buildFolderStore(m, nil, "mid-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	abs, writable, err := store.ResolvePath(aitools.RunFolderName, ".")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if !writable {
		t.Fatal("run folder must be writable")
	}
	if filepath.Base(abs) != "mid-abc" {
		t.Fatalf("run folder should be keyed by missionID, got %s", abs)
	}

	// Sidecar written with CleanupDays preserved
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

func TestBuildFolderStore_RunFolder_SidecarPreservedOnResume(t *testing.T) {
	dir := t.TempDir()
	m := &config.Mission{
		Name: "m",
		RunFolder: &config.MissionRunFolder{
			Base: filepath.Join(dir, "runs"),
		},
	}

	// First build: sidecar written
	if _, err := buildFolderStore(m, nil, "mid-1"); err != nil {
		t.Fatalf("first build: %v", err)
	}
	runDir := filepath.Join(dir, "runs", "mid-1")
	firstMetaBytes, _ := os.ReadFile(filepath.Join(runDir, runMetadataFile))
	var first runMetadata
	_ = json.Unmarshal(firstMetaBytes, &first)

	// Sleep a touch so a re-written timestamp would differ
	time.Sleep(10 * time.Millisecond)

	// Second build (same missionID = resume): sidecar must NOT be overwritten
	if _, err := buildFolderStore(m, nil, "mid-1"); err != nil {
		t.Fatalf("second build: %v", err)
	}
	secondMetaBytes, _ := os.ReadFile(filepath.Join(runDir, runMetadataFile))
	var second runMetadata
	_ = json.Unmarshal(secondMetaBytes, &second)

	if !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("CreatedAt should be preserved on resume: first=%v second=%v", first.CreatedAt, second.CreatedAt)
	}
}

func TestBuildFolderStore_RunFolder_RequiresMissionID(t *testing.T) {
	m := &config.Mission{
		Name: "m",
		RunFolder: &config.MissionRunFolder{
			Base: t.TempDir(),
		},
	}
	_, err := buildFolderStore(m, nil, "")
	if err == nil {
		t.Fatal("expected error when missionID is empty")
	}
}

func TestBuildFolderStore_RejectsReservedSharedFolderNames(t *testing.T) {
	for _, reserved := range []string{"mission", "run"} {
		m := &config.Mission{
			Name:    "m",
			Folders: []string{reserved},
		}
		shared := []config.SharedFolder{
			{Name: reserved, Path: t.TempDir()},
		}
		_, err := buildFolderStore(m, shared, "mid-1")
		if err == nil {
			t.Fatalf("expected error for reserved shared folder name %q", reserved)
		}
	}
}

func TestBuildFolderStore_BothMissionAndRunFolder(t *testing.T) {
	dir := t.TempDir()
	m := &config.Mission{
		Name: "m",
		Folder: &config.MissionFolder{
			Path: filepath.Join(dir, "persist"),
		},
		RunFolder: &config.MissionRunFolder{
			Base: filepath.Join(dir, "runs"),
		},
	}
	store, err := buildFolderStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err := store.ResolvePath(aitools.MissionFolderName, "."); err != nil {
		t.Fatalf("mission folder should resolve: %v", err)
	}
	if _, _, err := store.ResolvePath(aitools.RunFolderName, "."); err != nil {
		t.Fatalf("run folder should resolve: %v", err)
	}
}

func TestResolvePath_EmptyFolderNameRejected(t *testing.T) {
	dir := t.TempDir()
	m := &config.Mission{
		Name:   "m",
		Folder: &config.MissionFolder{Path: dir},
	}
	store, err := buildFolderStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, err := store.ResolvePath("", "."); err == nil {
		t.Fatal("expected error when folder name is empty")
	}
}

func TestResolvePath_RejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	m := &config.Mission{
		Name:   "m",
		Folder: &config.MissionFolder{Path: dir},
	}
	store, err := buildFolderStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, err := store.ResolvePath("mission", "../outside"); err == nil {
		t.Fatal("expected path-escape error")
	}
}

func TestResolvePath_UnknownFolder(t *testing.T) {
	dir := t.TempDir()
	m := &config.Mission{
		Name:   "m",
		Folder: &config.MissionFolder{Path: dir},
	}
	store, err := buildFolderStore(m, nil, "mid-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, err := store.ResolvePath("does_not_exist", "."); err == nil {
		t.Fatal("expected error for unknown folder")
	}
}

// --- SweepExpiredRunFolders ------------------------------------------------

// writeRun creates a run folder with a sidecar recording the given created_at
// and cleanup_days. Useful for driving the sweep.
func writeRun(t *testing.T, base, name string, createdAt time.Time, cleanupDays int) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	meta := runMetadata{
		Mission:     "m",
		MissionID:   name,
		CreatedAt:   createdAt,
		CleanupDays: cleanupDays,
	}
	b, _ := json.Marshal(&meta)
	if err := os.WriteFile(filepath.Join(dir, runMetadataFile), b, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSweepExpiredRunFolders_DeletesExpired(t *testing.T) {
	base := t.TempDir()
	expired := writeRun(t, base, "old", time.Now().Add(-8*24*time.Hour), 7)

	removed, err := SweepExpiredRunFolders(base)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected 1 removal, got %v", removed)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatalf("expired folder should be gone: %v", err)
	}
}

func TestSweepExpiredRunFolders_KeepsUnexpired(t *testing.T) {
	base := t.TempDir()
	fresh := writeRun(t, base, "new", time.Now().Add(-2*24*time.Hour), 7)

	removed, err := SweepExpiredRunFolders(base)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("expected nothing removed, got %v", removed)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh folder should still exist: %v", err)
	}
}

func TestSweepExpiredRunFolders_IgnoresZeroCleanup(t *testing.T) {
	base := t.TempDir()
	keep := writeRun(t, base, "forever", time.Now().Add(-365*24*time.Hour), 0)

	removed, err := SweepExpiredRunFolders(base)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("expected nothing removed when cleanup=0, got %v", removed)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("folder with cleanup=0 should be preserved: %v", err)
	}
}

func TestSweepExpiredRunFolders_IgnoresFoldersWithoutSidecar(t *testing.T) {
	base := t.TempDir()
	manual := filepath.Join(base, "hand_made")
	if err := os.MkdirAll(manual, 0755); err != nil {
		t.Fatal(err)
	}

	removed, err := SweepExpiredRunFolders(base)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("sweep must leave un-marked folders alone, got %v", removed)
	}
	if _, err := os.Stat(manual); err != nil {
		t.Fatalf("manually created folder should still exist: %v", err)
	}
}

func TestSweepExpiredRunFolders_MissingBaseIsNotAnError(t *testing.T) {
	base := filepath.Join(t.TempDir(), "does", "not", "exist")
	removed, err := SweepExpiredRunFolders(base)
	if err != nil {
		t.Fatalf("sweep should tolerate missing base: %v", err)
	}
	if removed != nil {
		t.Fatalf("expected nil removed, got %v", removed)
	}
}

// TestSweepThenRebuildRoundTrip mirrors the real flow: an old run folder
// exists with a sidecar backdated past its cleanup window, the sweep deletes
// it, then a new buildFolderStore (different missionID) creates a fresh run
// folder with a current sidecar — and the old one is gone.
func TestSweepThenRebuildRoundTrip(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runs")

	// Simulate a run from days ago that's past its cleanup deadline.
	stale := writeRun(t, base, "old-run", time.Now().Add(-5*24*time.Hour), 2)

	if _, err := SweepExpiredRunFolders(base); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale run should have been deleted: %v", err)
	}

	m := &config.Mission{
		Name: "folders_demo",
		RunFolder: &config.MissionRunFolder{
			Base:    base,
			Cleanup: 2,
		},
	}
	store, err := buildFolderStore(m, nil, "new-run")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	fresh, _, err := store.ResolvePath(aitools.RunFolderName, ".")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if filepath.Base(fresh) != "new-run" {
		t.Fatalf("fresh run should be keyed by new missionID, got %s", fresh)
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

func TestResolvedRunFolderBase(t *testing.T) {
	if got := ResolvedRunFolderBase(nil); got != DefaultRunFolderBase {
		t.Fatalf("nil rf: want %q, got %q", DefaultRunFolderBase, got)
	}
	if got := ResolvedRunFolderBase(&config.MissionRunFolder{}); got != DefaultRunFolderBase {
		t.Fatalf("empty base: want %q, got %q", DefaultRunFolderBase, got)
	}
	if got := ResolvedRunFolderBase(&config.MissionRunFolder{Base: "/custom"}); got != "/custom" {
		t.Fatalf("explicit base: want %q, got %q", "/custom", got)
	}
}

func TestBuildFolderStore_CreatesNestedBase(t *testing.T) {
	// Base directory with nested missing parents — MkdirAll should create them all.
	base := filepath.Join(t.TempDir(), "a", "b", "c", "runs")
	m := &config.Mission{
		Name: "m",
		RunFolder: &config.MissionRunFolder{
			Base: base,
		},
	}
	if _, err := buildFolderStore(m, nil, "mid-1"); err != nil {
		t.Fatalf("build: %v", err)
	}
	if info, err := os.Stat(filepath.Join(base, "mid-1")); err != nil || !info.IsDir() {
		t.Fatalf("nested run folder not created: %v", err)
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
