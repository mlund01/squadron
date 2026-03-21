package store

import (
	"fmt"
	"os"
	"path/filepath"

	"squadron/config"
)

// NewBundle creates a store Bundle based on the storage configuration
func NewBundle(cfg *config.StorageConfig) (*Bundle, error) {
	switch cfg.Backend {
	case "sqlite":
		// Ensure directory exists
		dir := filepath.Dir(cfg.Path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create storage directory %s: %w", dir, err)
		}
		return NewSQLiteBundle(cfg.Path)

	case "postgres":
		if cfg.ConnString == "" {
			return nil, fmt.Errorf("postgres backend requires conn_string to be set")
		}
		return NewPostgresBundle(cfg.ConnString)

	default:
		return nil, fmt.Errorf("unknown storage backend: %s (expected 'sqlite' or 'postgres')", cfg.Backend)
	}
}
