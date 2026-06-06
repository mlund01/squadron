package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// ProjectRootMarker is the prefix that anchors a path attribute to the
// project root (the directory passed to `squadron -c <dir>`) instead of
// the file's own directory. Example: `path = "@/data/kb"`.
const ProjectRootMarker = "@/"

// ResolveConfigPath resolves a user-supplied path attribute from HCL
// according to the project's anchoring rules. It is the single source of
// truth for path resolution on every block attribute that takes a path
// (packet.path, load(), and future block path attributes).
//
// projectRoot is the directory passed to `squadron -c <dir>` (also the
// configDir). hclFileDir is the directory containing the HCL file that
// declared this path attribute (used for relative paths so a sub-file
// inside the project can refer to its siblings). If hclFileDir is empty,
// projectRoot is used as the relative anchor.
//
// Rules:
//
//   - "@/foo"               → projectRoot + "foo"   (explicit project root)
//   - "/foo", absolute      → REJECTED              (no filesystem-root access)
//   - "./foo", "../foo"     → hclFileDir + path     (HCL-file-relative)
//   - "foo" (bare)          → hclFileDir + "foo"    (HCL-file-relative)
//
// After resolution, the path MUST be inside projectRoot. A `../../etc/...`
// style escape is rejected at parse time so an agent's read tool can't be
// tricked into wandering off the project tree.
//
// Returns the absolute, cleaned, contained path on success.
func ResolveConfigPath(projectRoot, hclFileDir, rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("path is empty")
	}
	if projectRoot == "" {
		return "", fmt.Errorf("project root is empty (internal error)")
	}
	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("project root %q: %w", projectRoot, err)
	}

	var resolved string
	switch {
	case strings.HasPrefix(rawPath, ProjectRootMarker):
		// "@/sub/dir" — strip the marker and anchor to project root.
		resolved = filepath.Join(rootAbs, strings.TrimPrefix(rawPath, ProjectRootMarker))
	case filepath.IsAbs(rawPath):
		return "", fmt.Errorf("path %q: absolute paths are not allowed — use a path relative to the HCL file or prefix with %q for the project root", rawPath, ProjectRootMarker)
	default:
		anchor := hclFileDir
		if anchor == "" {
			anchor = rootAbs
		}
		resolved = filepath.Join(anchor, rawPath)
	}

	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", rawPath, err)
	}

	// Containment check: the resolved path must be a STRICT descendant of
	// projectRoot. Two failure modes:
	//
	//   - rel == ".." or starts with "../" → escapes the project tree.
	//   - rel == "."                       → IS the project root itself.
	//     Pointing a config path attribute at the whole project root is
	//     almost certainly a mistake; for packet blocks specifically it
	//     also triggers HCL-exclusion of every config file and silently
	//     leaves the user with "0 missions found" rather than a clear
	//     error.
	//
	// Both are rejected with a descriptive message.
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the project root %q", rawPath, rootAbs)
	}
	if rel == "." {
		return "", fmt.Errorf("path %q resolves to the project root %q itself — config path attributes must point at a specific file or subdirectory, not the whole project", rawPath, rootAbs)
	}
	return abs, nil
}

// ResolveProjectPath resolves a path to absolute and verifies that it
// stays within the project root (the process CWD). It rejects any path
// that — after cleaning and joining — falls outside the root, so
// `../../etc/passwd`-style escapes are blocked at config-parse time.
//
// Absolute paths are accepted only if they happen to land inside the
// root.
//
// Deprecated: prefer ResolveConfigPath, which takes the project root and
// the HCL file's directory explicitly instead of relying on the process
// working directory.
func ResolveProjectPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}

	cleaned := filepath.Clean(path)
	var abs string
	if filepath.IsAbs(cleaned) {
		abs = cleaned
	} else {
		abs = filepath.Join(root, cleaned)
	}

	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside project root %q", path, root)
	}
	return abs, nil
}
