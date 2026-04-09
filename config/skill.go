package config

import "fmt"

// Skill represents a skill that can be loaded on-demand by agents.
type Skill struct {
	Name        string   `hcl:"name,label" json:"name"`
	Description string   `hcl:"description" json:"description"`
	Instructions     string   `hcl:"instructions" json:"instructions"`
	Tools       []string `hcl:"tools,optional" json:"tools,omitempty"`
}

// Validate checks that a skill has all required fields.
func (s *Skill) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	if s.Description == "" {
		return fmt.Errorf("description is required")
	}
	if s.Instructions == "" {
		return fmt.Errorf("instructions is required")
	}
	return nil
}
