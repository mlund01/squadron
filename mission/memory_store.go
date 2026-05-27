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

// On-disk layout under SquadronHome:
//
//	<squadron_home>/memories/shared/<name>/                 — shared memory
//	<squadron_home>/memories/mission/<mission_name>/        — mission memory
//	<squadron_home>/scratchpads/<mission_name>/<run_id>/    — mission scratchpad
const (
	memoriesSubdir   = "memories"
	scratchpadSubdir = "scratchpads"
)

// runMetadataFile is the sidecar written inside each materialized scratchpad
// directory so the cleanup sweep can tell when it was created.
const runMetadataFile = ".squadron-run.json"

type runMetadata struct {
	Mission     string    `json:"mission"`
	MissionID   string    `json:"mission_id"`
	CreatedAt   time.Time `json:"created_at"`
	CleanupDays int       `json:"cleanup_days,omitempty"`
}

// MemoriesRoot returns `<squadron_home>/memories`, the parent of every
// materialized memory slot (shared + per-mission).
func MemoriesRoot() (string, error) {
	home, err := paths.SquadronHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, memoriesSubdir), nil
}

// ScratchpadsRoot returns `<squadron_home>/scratchpads`, the parent of every
// materialized per-run scratchpad. Used by the cleanup sweep.
func ScratchpadsRoot() (string, error) {
	home, err := paths.SquadronHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, scratchpadSubdir), nil
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

// MissionMemoryPath returns the on-disk path for a mission's persistent
// memory slot. Stable across runs of the same mission.
func MissionMemoryPath(missionName string) (string, error) {
	root, err := MemoriesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "mission", missionName), nil
}

// MissionScratchpadPath returns the on-disk path for one run's scratchpad.
// Unique per mission run instance.
func MissionScratchpadPath(missionName, missionInstanceID string) (string, error) {
	root, err := ScratchpadsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, missionName, missionInstanceID), nil
}

type missionMemoryStore struct {
	slots map[string]*memorySlot
}

type memorySlot struct {
	absPath     string
	description string
}

// buildMemoryStore creates an aitools.MemoryStore from the mission config and
// the declared top-level memories. missionInstanceID scopes the scratchpad
// path; it must be non-empty when mission.Scratchpad is true. Returns nil if
// no slots are configured.
func buildMemoryStore(mission *config.Mission, memories []config.Memory, missionInstanceID string) (aitools.MemoryStore, error) {
	store := &missionMemoryStore{
		slots: make(map[string]*memorySlot),
	}

	memByName := make(map[string]*config.Memory)
	for i := range memories {
		memByName[memories[i].Name] = &memories[i]
	}

	for _, name := range mission.Memories {
		if name == config.MemorySlotName || name == config.ScratchpadSlotName {
			return nil, fmt.Errorf("shared memory %q uses a reserved slot name", name)
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
		store.slots[name] = &memorySlot{
			absPath:     absPath,
			description: mem.Description,
		}
	}

	if mission.Memory != nil {
		absPath, err := MissionMemoryPath(mission.Name)
		if err != nil {
			return nil, fmt.Errorf("memory: resolve path: %w", err)
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("memory: create directory: %w", err)
		}
		store.slots[config.MemorySlotName] = &memorySlot{
			absPath:     absPath,
			description: mission.Memory.Description,
		}
	}

	if mission.Scratchpad {
		if missionInstanceID == "" {
			return nil, fmt.Errorf("scratchpad requires a mission instance ID")
		}
		absPath, err := MissionScratchpadPath(mission.Name, missionInstanceID)
		if err != nil {
			return nil, fmt.Errorf("scratchpad: resolve path: %w", err)
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("scratchpad: create directory: %w", err)
		}
		if err := writeRunMetadata(absPath, mission.Name, missionInstanceID, config.ScratchpadCleanupDays); err != nil {
			return nil, fmt.Errorf("scratchpad: write metadata: %w", err)
		}
		store.slots[config.ScratchpadSlotName] = &memorySlot{
			absPath: absPath,
			// No user-supplied description — the agent prompt explains
			// what the scratchpad is for.
		}
	}

	if len(store.slots) == 0 {
		return nil, nil
	}

	return store, nil
}

// writeRunMetadata records when the scratchpad directory was created so the
// sweep can decide when to delete it. Uses O_CREATE|O_EXCL so concurrent
// starts and resumes never clobber the original timestamp — exactly one
// writer wins, others observe EEXIST and skip.
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

func (s *missionMemoryStore) ResolvePath(slotName string, relPath string) (string, error) {
	if slotName == "" {
		return "", fmt.Errorf("slot name is required (available: %v)", s.availableNames())
	}

	entry, ok := s.slots[slotName]
	if !ok {
		return "", fmt.Errorf("slot %q not found. Available: %v", slotName, s.availableNames())
	}

	cleaned := filepath.Clean(relPath)
	if cleaned == "." {
		return entry.absPath, nil
	}

	fullPath := filepath.Join(entry.absPath, cleaned)
	if !strings.HasPrefix(fullPath, entry.absPath) {
		return "", fmt.Errorf("path escapes slot root")
	}

	return fullPath, nil
}

func (s *missionMemoryStore) availableNames() []string {
	names := make([]string, 0, len(s.slots))
	for name := range s.slots {
		names = append(names, name)
	}
	return names
}

func (s *missionMemoryStore) MemoryInfos() []aitools.MemoryInfo {
	var infos []aitools.MemoryInfo
	for name, entry := range s.slots {
		infos = append(infos, aitools.MemoryInfo{
			Name:        name,
			Description: entry.description,
		})
	}
	return infos
}

// SweepExpiredScratchpads deletes any per-run scratchpad directory whose
// sidecar (.squadron-run.json) records a created_at older than its
// cleanup_days. Directories without a sidecar, or with cleanup_days == 0,
// are left alone.
//
// Walks `<squadron_home>/scratchpads/*/*` and considers every per-run
// directory — no per-mission filtering, so callers don't need to know which
// missions exist.
func SweepExpiredScratchpads() (removed []string, err error) {
	root, err := ScratchpadsRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
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
		runBase := filepath.Join(root, missionEntry.Name())
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
