package config

import (
	"os"
	"path/filepath"

	"squadron/internal/paths"
)

// StorageConfig defines the storage backend for mission state
type StorageConfig struct {
	Backend    string `hcl:"backend,optional"`     // "sqlite" or "postgres"
	Path       string `hcl:"path,optional"`        // SQLite file path (default: ".squadron/store.db")
	ConnString string `hcl:"conn_string,optional"` // Postgres connection string
}

// Defaults fills in default values for unset fields
func (s *StorageConfig) Defaults() {
	if s.Backend == "" {
		s.Backend = "sqlite"
	}
	if s.Path == "" {
		s.Path = ".squadron/store.db"
	}
}

// DefaultStorageConfig returns a storage config using sensible defaults,
// for use when the full config hasn't loaded yet.
func DefaultStorageConfig(configPath string) *StorageConfig {
	sc := &StorageConfig{}
	sc.Defaults()

	// When SQUADRON_HOME is set, store goes there
	if sqHome := os.Getenv("SQUADRON_HOME"); sqHome != "" {
		if home, err := paths.SquadronHome(); err == nil {
			sc.Path = filepath.Join(home, "store.db")
			return sc
		}
	}

	// Otherwise, relative to config path
	info, err := os.Stat(configPath)
	if err == nil {
		dir := configPath
		if !info.IsDir() {
			dir = filepath.Dir(configPath)
		}
		sc.Path = filepath.Join(dir, ".squadron", "store.db")
	}
	return sc
}
