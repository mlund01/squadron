package mission

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
// the declared top-level memories + contexts. missionInstanceID scopes the
// scratchpad path; it must be non-empty when mission.Scratchpad is true.
// Returns nil if no slots are configured.
//
// Contexts are registered under "context.<name>" keys (config.ContextSlotPrefix)
// so they share the slot namespace without colliding with memory names. The
// MemoryStore itself doesn't distinguish read-only from read-write; the file
// tools enforce the read-only / text-only policy based on the slot name
// prefix via aitools.IsContextSlot.
func buildMemoryStore(mission *config.Mission, memories []config.Memory, contexts []config.Context, missionInstanceID string) (aitools.MemoryStore, error) {
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

	// Collect context references from the mission level + every task. Tasks
	// share the mission-wide store, so per-task `contexts = [...]` is a
	// declaration of need (validated above) rather than a runtime access
	// boundary — every task that runs sees every referenced context.
	referencedContexts := make(map[string]bool)
	for _, n := range mission.Contexts {
		referencedContexts[n] = true
	}
	for _, t := range mission.Tasks {
		for _, n := range t.Contexts {
			referencedContexts[n] = true
		}
	}
	if len(referencedContexts) > 0 {
		ctxByName := make(map[string]*config.Context, len(contexts))
		for i := range contexts {
			ctxByName[contexts[i].Name] = &contexts[i]
		}
		for name := range referencedContexts {
			c, ok := ctxByName[name]
			if !ok {
				return nil, fmt.Errorf("context %q not found", name)
			}
			// Context paths are user-controlled. Resolve to absolute (idempotent
			// for paths Validate already absolutized) and register under the
			// "context.<name>" key so the file tools can apply the read-only /
			// text-only policy via IsContextSlot.
			absPath, err := paths.ResolveFolderPath(c.Path)
			if err != nil {
				return nil, fmt.Errorf("context %q: invalid path: %w", name, err)
			}
			store.slots[config.ContextSlotPrefix+name] = &memorySlot{
				absPath:     absPath,
				description: c.Description,
			}
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
	infos := make([]aitools.MemoryInfo, 0, len(s.slots))
	for name, entry := range s.slots {
		infos = append(infos, aitools.MemoryInfo{
			Name:        name,
			Description: entry.description,
		})
	}
	// Stable order — the result feeds the agent's system prompt
	// (prompts.FormatMemoryContext). Go map iteration is randomized, so
	// without this sort the prompt bytes change run-to-run and Anthropic
	// prompt caching misses on otherwise-identical missions.
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
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
			// Skip this mission on any IO error (permission denied, transient
			// stat failure, etc.) so one bad subdir doesn't halt the sweep
			// for every other mission. NotExist is just the empty case.
			continue
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
