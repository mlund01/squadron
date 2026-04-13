package mcp

import (
	"fmt"
	"path/filepath"

	"squadron/internal/paths"
)

// GetMCPDir returns the base directory where MCP server installs live.
// Sits next to ~/.squadron/plugins/.
func GetMCPDir() (string, error) {
	sqHome, err := paths.SquadronHome()
	if err != nil {
		return "", fmt.Errorf("failed to get squadron home: %w", err)
	}
	return filepath.Join(sqHome, "mcp", paths.PlatformDir()), nil
}

// GetServerDir returns the cache dir for a specific (name, version) pair.
// e.g. ~/.squadron/mcp/filesystem/2024.12.1/
func GetServerDir(name, version string) (string, error) {
	base, err := GetMCPDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, name, version), nil
}
