package config

import (
	"fmt"
	"os"

	"squadron/internal/paths"
)

type SharedFolder struct {
	Name        string `hcl:"name,label"`
	Path        string `hcl:"path"`
	Label       string `hcl:"label,optional"`
	Description string `hcl:"description,optional"`
	Editable    bool   `hcl:"editable,optional"`
}

func (fb *SharedFolder) Validate() error {
	if fb.Path == "" {
		return fmt.Errorf("path is required")
	}
	absPath, err := paths.ResolveFolderPath(fb.Path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("path does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path must be a directory")
	}
	return nil
}
