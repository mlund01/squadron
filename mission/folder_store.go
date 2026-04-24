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

// DefaultRunFolderBase is the parent directory used when a run_folder block
// does not specify `base`.
const DefaultRunFolderBase = ".squadron/runs"

// runMetadataFile is the sidecar written inside each materialized run folder
// so the cleanup sweep can tell when the folder was created.
const runMetadataFile = ".squadron-run.json"

type runMetadata struct {
	Mission     string    `json:"mission"`
	MissionID   string    `json:"mission_id"`
	CreatedAt   time.Time `json:"created_at"`
	CleanupDays int       `json:"cleanup_days,omitempty"`
}

// ResolvedRunFolderBase returns the base directory for a run_folder,
// substituting the default when unset.
func ResolvedRunFolderBase(rf *config.MissionRunFolder) string {
	if rf == nil || rf.Base == "" {
		return DefaultRunFolderBase
	}
	return rf.Base
}

// resolvedCleanup returns the cleanup window in days for a run_folder.
// Reads the parsed pointer when set (Validate fills in the default at config
// load time); falls back to the default for callers that hand-build a struct
// and skip Validate (notably tests).
func resolvedCleanup(rf *config.MissionRunFolder) int {
	if rf == nil || rf.Cleanup == nil {
		return config.DefaultRunFolderCleanupDays
	}
	return *rf.Cleanup
}

type missionFolderStore struct {
	folders map[string]*folderEntry
}

type folderEntry struct {
	absPath     string
	description string
	writable    bool
}

// buildFolderStore creates a FolderStore from the mission config.
// missionID scopes the per-run folder path; it must be non-empty when
// mission.RunFolder is set. Returns nil if no folders are configured.
func buildFolderStore(mission *config.Mission, sharedFolders []config.SharedFolder, missionID string) (aitools.FolderStore, error) {
	store := &missionFolderStore{
		folders: make(map[string]*folderEntry),
	}

	foldersByName := make(map[string]*config.SharedFolder)
	for i := range sharedFolders {
		foldersByName[sharedFolders[i].Name] = &sharedFolders[i]
	}

	for _, name := range mission.Folders {
		if name == aitools.MissionFolderName || name == aitools.RunFolderName {
			return nil, fmt.Errorf("shared folder %q uses a reserved name", name)
		}
		sf, ok := foldersByName[name]
		if !ok {
			return nil, fmt.Errorf("shared folder %q not found", name)
		}
		absPath, err := paths.ResolveFolderPath(sf.Path)
		if err != nil {
			return nil, fmt.Errorf("shared folder %q: invalid path: %w", name, err)
		}
		desc := sf.Description
		if desc == "" {
			desc = sf.Label
		}
		store.folders[name] = &folderEntry{
			absPath:     absPath,
			description: desc,
			writable:    sf.Editable,
		}
	}

	if mission.Folder != nil {
		absPath, err := paths.ResolveFolderPath(mission.Folder.Path)
		if err != nil {
			return nil, fmt.Errorf("mission folder: invalid path: %w", err)
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("mission folder: create directory: %w", err)
		}
		store.folders[aitools.MissionFolderName] = &folderEntry{
			absPath:     absPath,
			description: mission.Folder.Description,
			writable:    true,
		}
	}

	if mission.RunFolder != nil {
		if missionID == "" {
			return nil, fmt.Errorf("run_folder requires a mission ID")
		}
		absBase, err := paths.ResolveFolderPath(ResolvedRunFolderBase(mission.RunFolder))
		if err != nil {
			return nil, fmt.Errorf("run_folder: invalid base: %w", err)
		}
		absPath := filepath.Join(absBase, missionID)
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("run_folder: create directory: %w", err)
		}
		if err := writeRunMetadata(absPath, mission.Name, missionID, resolvedCleanup(mission.RunFolder)); err != nil {
			return nil, fmt.Errorf("run_folder: write metadata: %w", err)
		}
		store.folders[aitools.RunFolderName] = &folderEntry{
			absPath:     absPath,
			description: mission.RunFolder.Description,
			writable:    true,
		}
	}

	if len(store.folders) == 0 {
		return nil, nil
	}

	return store, nil
}

// writeRunMetadata records when the run folder was created so the sweep can
// decide when to delete it. Uses O_CREATE|O_EXCL so concurrent starts and
// resumes never clobber the original timestamp — exactly one writer wins,
// others observe EEXIST and skip.
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

// SweepExpiredRunFolders deletes any subfolder of base whose sidecar
// (.squadron-run.json) records a created_at older than its cleanup_days.
// Folders without a sidecar, or with cleanup_days == 0, are left alone.
// Missing base is not an error — returns (nil, nil).
func SweepExpiredRunFolders(base string) (removed []string, err error) {
	absBase, err := paths.ResolveFolderPath(base)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(absBase)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now().UTC()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runDir := filepath.Join(absBase, e.Name())
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
	return removed, nil
}
