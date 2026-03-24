package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SquadronHome returns the root directory for Squadron state.
// When SQUADRON_HOME is set, uses that directly.
// Otherwise falls back to ~/.squadron.
func SquadronHome() (string, error) {
	if dir := os.Getenv("SQUADRON_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".squadron"), nil
}

// ResolveFolderPath resolves a folder path, respecting SQUADRON_HOME when set.
// When SQUADRON_HOME is set (container mode):
//   - Absolute paths are rooted at $SQUADRON_HOME/folders/
//   - ~/ paths are rooted at $SQUADRON_HOME/folders/
//   - Relative paths are not allowed
//
// When SQUADRON_HOME is not set, returns the absolute path using normal resolution.
func ResolveFolderPath(path string) (string, error) {
	if os.Getenv("SQUADRON_HOME") != "" {
		sqHome, err := SquadronHome()
		if err != nil {
			return "", err
		}
		foldersRoot := filepath.Join(sqHome, "folders")

		if strings.HasPrefix(path, "~/") {
			return filepath.Join(foldersRoot, strings.TrimPrefix(path, "~/")), nil
		}
		if filepath.IsAbs(path) {
			return filepath.Join(foldersRoot, path), nil
		}
		return "", fmt.Errorf("relative paths not allowed when SQUADRON_HOME is set (use absolute or ~/ prefix): %s", path)
	}

	return filepath.Abs(path)
}
