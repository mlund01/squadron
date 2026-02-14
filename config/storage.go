package config

// StorageConfig defines the storage backend for mission state
type StorageConfig struct {
	Backend string `hcl:"backend,optional"` // "memory" or "sqlite"
	Path    string `hcl:"path,optional"`    // SQLite file path (default: ".squadron/store.db")
}

// Defaults fills in default values for unset fields
func (s *StorageConfig) Defaults() {
	if s.Backend == "" {
		s.Backend = "memory"
	}
	if s.Path == "" {
		s.Path = ".squadron/store.db"
	}
}
