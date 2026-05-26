package mission

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"squadron/aitools"
	"squadron/config"
	"squadron/internal/paths"
)

// memoriesSubdir is the directory under SquadronHome that holds every
// materialized memory slot. Layout:
//
//	<squadron_home>/memories/shared/<name>/
//	<squadron_home>/memories/mission/<mission_name>/persistent/
//	<squadron_home>/memories/mission/<mission_name>/run/<mission_instance_id>/
const memoriesSubdir = "memories"

// runMetadataFile is the sidecar written inside each materialized ephemeral
// memory directory so the cleanup sweep can tell when it was created.
const runMetadataFile = ".squadron-run.json"

type runMetadata struct {
	Mission     string    `json:"mission"`
	MissionID   string    `json:"mission_id"`
	CreatedAt   time.Time `json:"created_at"`
	CleanupDays int       `json:"cleanup_days,omitempty"`
}

// MemoriesRoot returns `<squadron_home>/memories`, the parent of every
// materialized memory slot. Exported so callers like the cleanup loop can
// pivot off the same base.
func MemoriesRoot() (string, error) {
	home, err := paths.SquadronHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, memoriesSubdir), nil
}

// SharedMemoryPath returns the on-disk path for a top-level shared memory
// named `name`.
func SharedMemoryPath(name string) (string, error) {
	root, err := MemoriesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "shared", name), nil
}

// PersistentMemoryPath returns the on-disk path for a mission's persistent
// memory slot. Path is stable across runs of the same mission.
func PersistentMemoryPath(missionName string) (string, error) {
	root, err := MemoriesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "mission", missionName, "persistent"), nil
}

// EphemeralMemoryPath returns the on-disk path for one run's ephemeral
// memory slot. Path is unique per mission run instance.
func EphemeralMemoryPath(missionName, missionInstanceID string) (string, error) {
	root, err := MemoriesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "mission", missionName, "run", missionInstanceID), nil
}

// MissionRunRoot returns `<memories_root>/mission/<name>/run`, the parent
// dir of every ephemeral memory directory for a given mission. Used by the
// cleanup sweep.
func MissionRunRoot(missionName string) (string, error) {
	root, err := MemoriesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "mission", missionName, "run"), nil
}

// resolvedEphemeralCleanup returns the cleanup window in days for an
// ephemeral mission memory. Reads the parsed pointer when set (Validate /
// the config parser fill in the default at config load time); falls back to
// the default for callers that hand-build a struct and skip Validate
// (notably tests).
func resolvedEphemeralCleanup(mm *config.MissionMemory) int {
	if mm == nil || mm.Cleanup == nil {
		return config.DefaultEphemeralCleanupDays
	}
	return *mm.Cleanup
}

type missionFolderStore struct {
	folders map[string]*folderEntry
}

type folderEntry struct {
	absPath     string
	description string
	writable    bool
}

// buildFolderStore creates a FolderStore from the mission config and the
// declared top-level memories. missionInstanceID scopes the ephemeral memory
// path; it must be non-empty when mission.EphemeralMemory is set. Returns
// nil if no memory slots are configured.
func buildFolderStore(mission *config.Mission, memories []config.Memory, missionInstanceID string) (aitools.FolderStore, error) {
	store := &missionFolderStore{
		folders: make(map[string]*folderEntry),
	}

	memByName := make(map[string]*config.Memory)
	for i := range memories {
		memByName[memories[i].Name] = &memories[i]
	}

	for _, name := range mission.Memories {
		if name == config.PersistentSlotName || name == config.EphemeralSlotName {
			return nil, fmt.Errorf("shared memory %q uses a reserved name", name)
		}
		mem, ok := memByName[name]
		if !ok {
			return nil, fmt.Errorf("shared memory %q not found", name)
		}
		absPath, err := SharedMemoryPath(name)
		if err != nil {
			return nil, fmt.Errorf("shared memory %q: resolve path: %w", name, err)
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("shared memory %q: create directory: %w", name, err)
		}
		desc := mem.Description
		if desc == "" {
			desc = mem.Label
		}
		store.folders[name] = &folderEntry{
			absPath:     absPath,
			description: desc,
			writable:    mem.Editable,
		}
	}

	if mission.PersistentMemory != nil {
		absPath, err := PersistentMemoryPath(mission.Name)
		if err != nil {
			return nil, fmt.Errorf("persistent memory: resolve path: %w", err)
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("persistent memory: create directory: %w", err)
		}
		store.folders[config.PersistentSlotName] = &folderEntry{
			absPath:     absPath,
			description: mission.PersistentMemory.Description,
			writable:    true,
		}
	}

	if mission.EphemeralMemory != nil {
		if missionInstanceID == "" {
			return nil, fmt.Errorf("ephemeral memory requires a mission instance ID")
		}
		absPath, err := EphemeralMemoryPath(mission.Name, missionInstanceID)
		if err != nil {
			return nil, fmt.Errorf("ephemeral memory: resolve path: %w", err)
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("ephemeral memory: create directory: %w", err)
		}
		if err := writeRunMetadata(absPath, mission.Name, missionInstanceID, resolvedEphemeralCleanup(mission.EphemeralMemory)); err != nil {
			return nil, fmt.Errorf("ephemeral memory: write metadata: %w", err)
		}
		store.folders[config.EphemeralSlotName] = &folderEntry{
			absPath:     absPath,
			description: mission.EphemeralMemory.Description,
			writable:    true,
		}
	}

	if len(store.folders) == 0 {
		return nil, nil
	}

	return store, nil
}

// writeRunMetadata records when the ephemeral memory directory was created
// so the sweep can decide when to delete it. Uses O_CREATE|O_EXCL so
// concurrent starts and resumes never clobber the original timestamp —
// exactly one writer wins, others observe EEXIST and skip.
func writeRunMetadata(dir, missionName, missionID string, cleanupDays int) error {
	path := filepath.Join(dir, runMetadataFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	meta := runMetadata{
		Mission:     missionName,
		MissionID:   missionID,
		CreatedAt:   time.Now().UTC(),
		CleanupDays: cleanupDays,
	}
	b, err := json.MarshalIndent(&meta, "", "  ")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	return err
}

func (s *missionFolderStore) ResolvePath(folderName string, relPath string) (string, bool, error) {
	if folderName == "" {
		return "", false, fmt.Errorf("folder name is required (available: %v)", s.availableNames())
	}

	entry, ok := s.folders[folderName]
	if !ok {
		return "", false, fmt.Errorf("folder %q not found. Available: %v", folderName, s.availableNames())
	}

	cleaned := filepath.Clean(relPath)
	if cleaned == "." {
		return entry.absPath, entry.writable, nil
	}

	fullPath := filepath.Join(entry.absPath, cleaned)
	if !strings.HasPrefix(fullPath, entry.absPath) {
		return "", false, fmt.Errorf("path escapes folder root")
	}

	return fullPath, entry.writable, nil
}

func (s *missionFolderStore) availableNames() []string {
	names := make([]string, 0, len(s.folders))
	for name := range s.folders {
		names = append(names, name)
	}
	return names
}

func (s *missionFolderStore) FolderInfos() []aitools.FolderInfo {
	var infos []aitools.FolderInfo
	for name, entry := range s.folders {
		infos = append(infos, aitools.FolderInfo{
			Name:        name,
			Description: entry.description,
			Writable:    entry.writable,
		})
	}
	return infos
}

// SweepExpiredEphemeralMemories deletes any per-run ephemeral memory
// directory whose sidecar (.squadron-run.json) records a created_at older
// than its cleanup_days. Directories without a sidecar, or with
// cleanup_days == 0, are left alone.
//
// It walks `<memories_root>/mission/*/run/*` and considers every
// per-run directory it finds — there's no per-mission filtering, so callers
// don't need to know which missions exist.
func SweepExpiredEphemeralMemories() (removed []string, err error) {
	root, err := MemoriesRoot()
	if err != nil {
		return nil, err
	}
	missionsBase := filepath.Join(root, "mission")
	entries, err := os.ReadDir(missionsBase)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now().UTC()
	for _, missionEntry := range entries {
		if !missionEntry.IsDir() {
			continue
		}
		runBase := filepath.Join(missionsBase, missionEntry.Name(), "run")
		runEntries, err := os.ReadDir(runBase)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		for _, e := range runEntries {
			if !e.IsDir() {
				continue
			}
			runDir := filepath.Join(runBase, e.Name())
			metaPath := filepath.Join(runDir, runMetadataFile)
			b, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta runMetadata
			if err := json.Unmarshal(b, &meta); err != nil {
				continue
			}
			if meta.CleanupDays <= 0 {
				continue
			}
			deadline := meta.CreatedAt.Add(time.Duration(meta.CleanupDays) * 24 * time.Hour)
			if now.Before(deadline) {
				continue
			}
			if err := os.RemoveAll(runDir); err != nil {
				continue
			}
			removed = append(removed, runDir)
		}
	}
	return removed, nil
}
