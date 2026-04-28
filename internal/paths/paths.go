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
//
// Resolution order:
//
//  1. A path previously set via SetHome (called by config-aware commands
//     based on their --config / -c value or the --squadron-home flag).
//  2. The SQUADRON_HOME environment variable.
//  3. .squadron in the current working directory (backstop for commands
//     like `squadron init` run in an empty dir).
//
// The result is cached so later calls are immune to cwd changes.
func SquadronHome() (string, error) {
	homeOnce.Do(func() {
		if env := os.Getenv("SQUADRON_HOME"); env != "" {
			cachedHome, homeErr = filepath.Abs(env)
			return
		}
		cwd, err := os.Getwd()
		if err != nil {
			homeErr = err
			return
		}
		cachedHome = filepath.Join(cwd, ".squadron")
	})
	return cachedHome, homeErr
}

// SetHome seeds SquadronHome with an explicit absolute path. Intended
// for CLI commands that know a specific config directory (-c) or
// override (--squadron-home): they resolve the path once in their
// PersistentPreRun and call SetHome before any state-touching code runs.
//
// SetHome only takes effect if SquadronHome has not yet resolved —
// callers at program start see the new value, but any call that already
// materialized the cache wins. (In practice, CLI commands call SetHome
// before touching vault / DB / plugins, so this is fine.)
func SetHome(path string) error {
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	homeOnce.Do(func() {
		cachedHome = abs
	})
	return nil
}

// ResolveHome picks the .squadron/ directory a config-aware command
// should use, given the command's --config/-c value and an optional
// --squadron-home override. It does not touch the cache; callers still
// need to pass the result to SetHome.
//
// Priority: explicit override > SQUADRON_HOME env > <configPath>/.squadron
// > cwd/.squadron.
func ResolveHome(configPath, override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	if env := os.Getenv("SQUADRON_HOME"); env != "" {
		return filepath.Abs(env)
	}
	if configPath != "" {
		abs, err := filepath.Abs(configPath)
		if err != nil {
			return "", err
		}
		return filepath.Join(abs, ".squadron"), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".squadron"), nil
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
