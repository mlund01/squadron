package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// StorageConfig defines the storage backend for mission state
type StorageConfig struct {
	Backend    string `hcl:"backend,optional"`     // "sqlite" or "postgres"
	Path       string `hcl:"path,optional"`        // SQLite file path (default: ".squadron/store.db")
	ConnString string `hcl:"conn_string,optional"` // Postgres connection string
	TTLDays    int    `hcl:"ttl_days,optional"`    // Purge finished mission records after N days; 0 = keep forever
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

// Validate checks storage settings. ttl_days of 0 (the default) disables
// retention; negative values are rejected.
func (s *StorageConfig) Validate() error {
	if s.TTLDays < 0 {
		return fmt.Errorf("ttl_days must be >= 0, got %d", s.TTLDays)
	}
	return nil
}

// DefaultStorageConfig returns a storage config using sensible defaults,
// for use when the full config hasn't loaded yet.
func DefaultStorageConfig(configPath string) *StorageConfig {
	sc := &StorageConfig{}
	sc.Defaults()

	// Relative to config path
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
