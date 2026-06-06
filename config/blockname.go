package config

import (
	"fmt"
	"regexp"
)

// blockNamePattern matches valid HCL block labels: lowercase letters, digits,
// and underscores only, and must not start with a digit. Block labels become
// HCL reference identifiers (e.g. models.anthropic, agents.browser), so they
// have to be valid identifiers.
var blockNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// validateBlockName enforces the naming rule for HCL block labels: only
// lowercase letters, digits, and underscores are allowed, and the name may not
// begin with a digit. kind is the block type used to frame the error message.
func validateBlockName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	if !blockNamePattern.MatchString(name) {
		return fmt.Errorf("%s name %q is invalid: only lowercase letters, digits, and underscores are allowed (and the name must not start with a digit)", kind, name)
	}
	return nil
}

// validateBlockNames checks the label of every named block in the config —
// top-level blocks and mission-scoped sub-blocks alike.
func (c *Config) validateBlockNames() error {
	for _, v := range c.Variables {
		if err := validateBlockName("variable", v.Name); err != nil {
			return err
		}
	}
	for _, m := range c.Models {
		if err := validateBlockName("model", m.Name); err != nil {
			return err
		}
	}
	for _, p := range c.Plugins {
		if err := validateBlockName("plugin", p.Name); err != nil {
			return err
		}
	}
	for _, s := range c.MCPServers {
		if err := validateBlockName("mcp", s.Name); err != nil {
			return err
		}
	}
	for _, t := range c.CustomTools {
		if err := validateBlockName("tool", t.Name); err != nil {
			return err
		}
	}
	for _, a := range c.Agents {
		if err := validateBlockName("agent", a.Name); err != nil {
			return err
		}
		for _, ls := range a.LocalSkills {
			if err := validateBlockName("skill", ls.Name); err != nil {
				return err
			}
		}
	}
	for _, s := range c.Skills {
		if err := validateBlockName("skill", s.Name); err != nil {
			return err
		}
	}
	for _, m := range c.Memories {
		if err := validateBlockName("memory", m.Name); err != nil {
			return err
		}
	}
	for _, m := range c.Missions {
		if err := validateBlockName("mission", m.Name); err != nil {
			return err
		}
		for _, t := range m.Tasks {
			if err := validateBlockName("task", t.Name); err != nil {
				return fmt.Errorf("mission '%s': %w", m.Name, err)
			}
		}
		for _, d := range m.Datasets {
			if err := validateBlockName("dataset", d.Name); err != nil {
				return fmt.Errorf("mission '%s': %w", m.Name, err)
			}
		}
		for _, a := range m.LocalAgents {
			if err := validateBlockName("agent", a.Name); err != nil {
				return fmt.Errorf("mission '%s': %w", m.Name, err)
			}
			for _, ls := range a.LocalSkills {
				if err := validateBlockName("skill", ls.Name); err != nil {
					return fmt.Errorf("mission '%s' agent '%s': %w", m.Name, a.Name, err)
				}
			}
		}
	}
	return nil
}
