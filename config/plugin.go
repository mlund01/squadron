package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Plugin represents a plugin configuration
type Plugin struct {
	Name     string            `hcl:"name,label"`
	Source   string            `hcl:"source,optional"`
	Version  string            `hcl:"version"`
	Settings map[string]string `hcl:"-"` // Parsed manually from settings block
}

// semverRegex matches semantic versioning strings like v1.0.0, v0.1.0-beta, etc.
// Also allows "local" for locally built plugins
var semverRegex = regexp.MustCompile(`^(local|v?\d+\.\d+\.\d+(-[a-zA-Z0-9.-]+)?(\+[a-zA-Z0-9.-]+)?)$`)

// IsLocalSource reports whether Source points at a local Go package on
// disk (vs. a remote GitHub release). Anything that doesn't start with
// "github.com/" is treated as a local path.
func (p *Plugin) IsLocalSource() bool {
	return p.Source != "" && !strings.HasPrefix(p.Source, "github.com/")
}

// Validate checks that the plugin configuration is valid. For local
// sources it also enforces the version=="local" coupling and confirms
// that Source has been resolved to an absolute path. Path resolution
// itself happens in the loader (paths.ResolveConfigPath) so this method
// can stay idempotent and free of CWD dependence.
func (p *Plugin) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("plugin name is required")
	}

	// Check for reserved plugin namespaces
	if IsReservedBuiltinNamespace(p.Name) {
		return fmt.Errorf("plugin name '%s' is reserved for internal tools", p.Name)
	}

	if p.Source == "" && !p.IsLocal() {
		return fmt.Errorf("plugin source is required (unless version is 'local')")
	}

	if p.Version == "" {
		return fmt.Errorf("plugin version is required")
	}

	if !semverRegex.MatchString(p.Version) {
		return fmt.Errorf("invalid version '%s': must be 'local' or semantic version (e.g., v1.0.0)", p.Version)
	}

	if p.IsLocalSource() {
		if p.Version != "local" {
			return fmt.Errorf("plugin %q: local source requires version = \"local\", got %q", p.Name, p.Version)
		}
		// Loader is responsible for resolving Source via
		// paths.ResolveConfigPath before Validate runs — surface a
		// loader-bug error rather than silently falling back to CWD.
		if !filepath.IsAbs(p.Source) {
			return fmt.Errorf("plugin %q: source %q must be resolved to absolute before Validate (loader bug)", p.Name, p.Source)
		}
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
