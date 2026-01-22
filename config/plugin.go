package config

import (
	"fmt"
	"regexp"
)

// Plugin represents a plugin configuration
type Plugin struct {
	Name    string `hcl:"name,label"`
	Source  string `hcl:"source"`
	Version string `hcl:"version"`
}

// semverRegex matches semantic versioning strings like v1.0.0, v0.1.0-beta, etc.
// Also allows "local" for locally built plugins
var semverRegex = regexp.MustCompile(`^(local|v?\d+\.\d+\.\d+(-[a-zA-Z0-9.-]+)?(\+[a-zA-Z0-9.-]+)?)$`)

// Validate checks that the plugin configuration is valid
func (p *Plugin) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("plugin name is required")
	}

	// Check for reserved plugin namespaces
	if IsReservedPluginNamespace(p.Name) {
		return fmt.Errorf("plugin name '%s' is reserved for internal tools", p.Name)
	}

	if p.Source == "" {
		return fmt.Errorf("plugin source is required")
	}

	if p.Version == "" {
		return fmt.Errorf("plugin version is required")
	}

	if !semverRegex.MatchString(p.Version) {
		return fmt.Errorf("invalid version '%s': must be 'local' or semantic version (e.g., v1.0.0)", p.Version)
	}

	return nil
}

// IsLocal returns true if this is a locally built plugin
func (p *Plugin) IsLocal() bool {
	return p.Version == "local"
}

// GetVersion returns the version string, normalizing it if needed
func (p *Plugin) GetVersion() string {
	return p.Version
}
