package mcp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"squadron/internal/release"
)

const runnerFileName = "runner.json"

// runnerConfig is persisted at <cacheDir>/runner.json. Its existence marks a
// successful install; readers short-circuit the installer.
type runnerConfig struct {
	Kind    string `json:"kind"`              // "npm" | "github"
	Runtime string `json:"runtime,omitempty"` // "node" for npm; "" for github
	Entry   string `json:"entry"`             // absolute path to the file to execute
	Package string `json:"package,omitempty"` // npm package name, for diagnostics
}

// resolveRunner materializes an install for a source-backed mcp spec and
// returns the spawn metadata. Cached installs are reused based on the presence
// of runner.json in the cache dir.
func resolveRunner(name, version, source, entry string) (*runnerConfig, error) {
	if version == "" {
		return nil, fmt.Errorf("mcp %q: version is required when source is set", name)
	}

	cacheDir, err := GetServerDir(name, version)
	if err != nil {
		return nil, err
	}

	if cfg, ok := readRunnerConfig(cacheDir); ok {
		return cfg, nil
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("mcp %q: create cache dir: %w", name, err)
	}

	var cfg *runnerConfig
	switch {
	case strings.HasPrefix(source, "npm:"):
		if entry != "" {
			return nil, fmt.Errorf("mcp %q: entry is only valid with github sources", name)
		}
		cfg, err = installNPM(cacheDir, strings.TrimPrefix(source, "npm:"), version)
	case strings.HasPrefix(source, "github.com/"):
		cfg, err = installGitHub(cacheDir, source, version, entry)
	default:
		return nil, fmt.Errorf("mcp %q: unsupported source %q (expected npm:<pkg> or github.com/owner/repo)", name, source)
	}
	if err != nil {
		return nil, fmt.Errorf("mcp %q: install: %w", name, err)
	}

	if err := writeRunnerConfig(cacheDir, cfg); err != nil {
		return nil, fmt.Errorf("mcp %q: write runner.json: %w", name, err)
	}
	return cfg, nil
}

func readRunnerConfig(dir string) (*runnerConfig, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, runnerFileName))
	if err != nil {
		return nil, false
	}
	var cfg runnerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, false
	}
	if cfg.Entry == "" {
		return nil, false
	}
	return &cfg, true
}

func writeRunnerConfig(dir string, cfg *runnerConfig) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, runnerFileName), raw, 0644)
}

// installNPM shells out to `npm install --prefix <cacheDir> <pkg>@<version>`
// and resolves the entry point from the installed package.json's "bin" field.
func installNPM(cacheDir, pkg, version string) (*runnerConfig, error) {
	if _, err := exec.LookPath("npm"); err != nil {
		return nil, fmt.Errorf("npm not found on PATH — install Node.js to use npm-sourced MCP servers")
	}
	if _, err := exec.LookPath("node"); err != nil {
		return nil, fmt.Errorf("node not found on PATH — install Node.js to use npm-sourced MCP servers")
	}

	spec := pkg + "@" + version
	cmd := exec.Command("npm", "install",
		"--prefix", cacheDir,
		"--no-save",
		"--no-package-lock",
		"--silent",
		spec,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrTxt := strings.TrimSpace(stderr.String())
		if stderrTxt != "" {
			return nil, fmt.Errorf("npm install %s failed: %w\n%s", spec, err, stderrTxt)
		}
		return nil, fmt.Errorf("npm install %s failed: %w", spec, err)
	}

	pkgDir := filepath.Join(cacheDir, "node_modules", filepath.FromSlash(pkg))
	entry, err := resolveNPMBin(pkgDir, pkg)
	if err != nil {
		return nil, err
	}

	return &runnerConfig{
		Kind:    "npm",
		Runtime: "node",
		Entry:   entry,
		Package: pkg,
	}, nil
}

// resolveNPMBin inspects pkgDir/package.json and returns the absolute path to
// the entry script referenced in the "bin" field.
func resolveNPMBin(pkgDir, pkg string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return "", fmt.Errorf("read package.json for %s: %w", pkg, err)
	}
	var meta struct {
		Name string          `json:"name"`
		Bin  json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", fmt.Errorf("parse package.json for %s: %w", pkg, err)
	}
	if len(meta.Bin) == 0 || string(meta.Bin) == "null" {
		return "", fmt.Errorf("npm package %s has no bin entry — cannot be used as an MCP server", pkg)
	}

	// bin may be a string or an object.
	var binStr string
	if err := json.Unmarshal(meta.Bin, &binStr); err == nil {
		return filepath.Join(pkgDir, filepath.FromSlash(binStr)), nil
	}

	var binMap map[string]string
	if err := json.Unmarshal(meta.Bin, &binMap); err != nil {
		return "", fmt.Errorf("parse bin field of %s: %w", pkg, err)
	}
	if len(binMap) == 0 {
		return "", fmt.Errorf("npm package %s has empty bin map", pkg)
	}

	// Prefer the entry whose key matches the package's bare name.
	bareName := pkg
	if idx := strings.LastIndex(bareName, "/"); idx >= 0 {
		bareName = bareName[idx+1:]
	}
	if v, ok := binMap[bareName]; ok {
		return filepath.Join(pkgDir, filepath.FromSlash(v)), nil
	}
	// Otherwise take the first entry in sorted order for determinism.
	keys := make([]string, 0, len(binMap))
	for k := range binMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return filepath.Join(pkgDir, filepath.FromSlash(binMap[keys[0]])), nil
}

// installGitHub downloads and extracts a GitHub release archive, then picks
// the entry executable (user-supplied via `entry` or auto-detected as the
// single executable file in the extracted tree).
func installGitHub(cacheDir, source, version, entryOverride string) (*runnerConfig, error) {
	src, err := release.ParseGitHubSource(source)
	if err != nil {
		return nil, err
	}

	// Extract everything — we'll pick the entry afterwards.
	keepAll := func(name string) string {
		return name
	}
	count, err := release.DownloadAndExtract(src, version, cacheDir, keepAll)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("release archive for %s@%s was empty", source, version)
	}

	entry, err := pickEntry(cacheDir, entryOverride)
	if err != nil {
		return nil, err
	}

	return &runnerConfig{
		Kind:  "github",
		Entry: entry,
	}, nil
}

// pickEntry resolves the binary to execute inside an extracted github archive.
// If the user specified `entry`, that path is honored (relative to extracted
// dir). Otherwise we scan for executable regular files and require exactly one.
func pickEntry(dir, override string) (string, error) {
	if override != "" {
		clean := filepath.Clean(override)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return "", fmt.Errorf("entry %q must be a relative path inside the archive", override)
		}
		full := filepath.Join(dir, clean)
		info, err := os.Stat(full)
		if err != nil {
			return "", fmt.Errorf("entry %q not found in extracted archive", override)
		}
		if info.IsDir() {
			return "", fmt.Errorf("entry %q is a directory, not a file", override)
		}
		// Ensure it's executable; plugins may need chmod on extracted files.
		if info.Mode().Perm()&0111 == 0 {
			if err := os.Chmod(full, info.Mode().Perm()|0755); err != nil {
				return "", fmt.Errorf("chmod entry %q: %w", override, err)
			}
		}
		return full, nil
	}

	var candidates []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("scan extracted archive: %w", err)
	}

	// Filter out our own runner.json if it happens to be executable.
	filtered := candidates[:0]
	for _, c := range candidates {
		if filepath.Base(c) == runnerFileName {
			continue
		}
		filtered = append(filtered, c)
	}
	candidates = filtered

	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("no executable file found in extracted archive — set `entry = \"...\"` or ensure the archive contains an executable binary")
	case 1:
		return candidates[0], nil
	default:
		rels := make([]string, 0, len(candidates))
		for _, c := range candidates {
			rel, rerr := filepath.Rel(dir, c)
			if rerr != nil {
				rel = c
			}
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		return "", fmt.Errorf("multiple executables in extracted archive — set `entry = \"...\"` to disambiguate. candidates: %s", strings.Join(rels, ", "))
	}
}
