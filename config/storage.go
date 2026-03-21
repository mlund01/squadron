package config

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
