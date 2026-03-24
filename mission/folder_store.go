package mission

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"squadron/aitools"
	"squadron/config"
	"squadron/internal/paths"
)

// missionFolderStore provides folder access for a mission.
// It resolves shared folder references and the optional dedicated mission folder.
type missionFolderStore struct {
	folders   map[string]*folderEntry // name → entry
	dedicated string                  // name of dedicated folder ("" if none)
}

type folderEntry struct {
	absPath     string
	description string
	writable    bool
	isDedicated bool
}

// buildFolderStore creates a FolderStore from the mission config.
// Returns nil if no folders are configured.
func buildFolderStore(mission *config.Mission, sharedFolders []config.SharedFolder) (aitools.FolderStore, error) {
	store := &missionFolderStore{
		folders: make(map[string]*folderEntry),
	}

	// Add shared folder references
	foldersByName := make(map[string]*config.SharedFolder)
	for i := range sharedFolders {
		foldersByName[sharedFolders[i].Name] = &sharedFolders[i]
	}

	for _, name := range mission.Folders {
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

	// Add dedicated mission folder
	if mission.Folder != nil {
		absPath, err := paths.ResolveFolderPath(mission.Folder.Path)
		if err != nil {
			return nil, fmt.Errorf("mission folder: invalid path: %w", err)
		}
		// Create the directory if it doesn't exist
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("mission folder: create directory: %w", err)
		}
		name := mission.Name
		store.dedicated = name
		store.folders[name] = &folderEntry{
			absPath:     absPath,
			description: mission.Folder.Description,
			writable:    true,
			isDedicated: true,
		}
	}

	// Return nil if no folders configured
	if len(store.folders) == 0 {
		return nil, nil
	}

	return store, nil
}

func (s *missionFolderStore) ResolvePath(folderName string, relPath string) (string, bool, error) {
	// Default to dedicated folder if no name specified
	if folderName == "" {
		if s.dedicated == "" {
			return "", false, fmt.Errorf("no default folder configured; specify a folder name")
		}
		folderName = s.dedicated
	}

	entry, ok := s.folders[folderName]
	if !ok {
		available := make([]string, 0, len(s.folders))
		for name := range s.folders {
			available = append(available, name)
		}
		return "", false, fmt.Errorf("folder %q not found. Available: %v", folderName, available)
	}

	// Resolve relative path
	cleaned := filepath.Clean(relPath)
	if cleaned == "." {
		return entry.absPath, entry.writable, nil
	}

	fullPath := filepath.Join(entry.absPath, cleaned)
	// Ensure the resolved path stays within the folder root
	if !strings.HasPrefix(fullPath, entry.absPath) {
		return "", false, fmt.Errorf("path escapes folder root")
	}

	return fullPath, entry.writable, nil
}

func (s *missionFolderStore) DefaultFolder() string {
	return s.dedicated
}

func (s *missionFolderStore) FolderInfos() []aitools.FolderInfo {
	var infos []aitools.FolderInfo
	for name, entry := range s.folders {
		infos = append(infos, aitools.FolderInfo{
			Name:        name,
			Description: entry.description,
			Writable:    entry.writable,
			IsDedicated: entry.isDedicated,
		})
	}
	return infos
}
