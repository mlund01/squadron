package config

import (
	"fmt"
)

// Gateway is the parsed `gateway "name" { ... }` HCL block. Squadron
// supports at most one gateway per instance — enforced at parse time.
type Gateway struct {
	Name     string            `hcl:"name,label"`
	Source   string            `hcl:"source,optional"`
	Version  string            `hcl:"version"`
	Settings map[string]string `hcl:"-"` // parsed manually from the settings block/attribute
}

func (g *Gateway) Validate() error {
	if g.Name == "" {
		return fmt.Errorf("gateway name is required")
	}
	if g.Source == "" && g.Version != "local" {
		return fmt.Errorf("gateway %q: source is required (unless version is 'local')", g.Name)
	}
	if g.Version == "" {
		return fmt.Errorf("gateway %q: version is required", g.Name)
	}
	if !semverRegex.MatchString(g.Version) {
		return fmt.Errorf("gateway %q: invalid version %q: must be 'local' or semantic version (e.g., v1.0.0)", g.Name, g.Version)
	}
	return nil
}

// IsLocal — version="local" skips the GitHub download path and uses
// the binary already present in the cache.
func (g *Gateway) IsLocal() bool { return g.Version == "local" }
