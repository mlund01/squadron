package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

var (
	cachedHome string
	homeOnce   sync.Once
	homeErr    error
)

// SquadronHome returns the root directory for Squadron state.
// Returns .squadron in the working directory at the time of first call.
// The result is cached so later calls are immune to cwd changes.
func SquadronHome() (string, error) {
	homeOnce.Do(func() {
		cwd, err := os.Getwd()
		if err != nil {
			homeErr = err
			return
		}
		cachedHome = filepath.Join(cwd, ".squadron")
	})
	return cachedHome, homeErr
}

// ResetHome clears the cached home path. Only for use in tests.
func ResetHome() {
	homeOnce = sync.Once{}
	cachedHome = ""
	homeErr = nil
}

// EnsureHome creates the .squadron directory if it doesn't exist.
func EnsureHome() error {
	home, err := SquadronHome()
	if err != nil {
		return err
	}
	return os.MkdirAll(home, 0700)
}

// PlatformDir returns a platform-specific subdirectory name (e.g. "darwin-arm64", "linux-amd64").
// Used for OS/arch-specific artifacts like plugins, MCP binaries, and command-center.
func PlatformDir() string {
	return fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
}

// ResolveFolderPath resolves a folder path to an absolute path.
func ResolveFolderPath(path string) (string, error) {
	return filepath.Abs(path)
}
