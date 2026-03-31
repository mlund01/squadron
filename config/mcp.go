package config

import "fmt"

// MCPConfig defines settings for the built-in MCP (Model Context Protocol) server.
// If no mcp block is present in config, the MCP server is not started.
type MCPConfig struct {
	Enabled bool   `hcl:"enabled,optional"`
	Port    int    `hcl:"port,optional"`
	Secret  string `hcl:"secret,optional"`
}

// Defaults fills in default values for unset fields
func (c *MCPConfig) Defaults() {
	if c.Port <= 0 {
		c.Port = 8090
	}
}

// Validate checks that the config is valid
func (c *MCPConfig) Validate() error {
	if c.Port < 1024 || c.Port > 65535 {
		return fmt.Errorf("mcp port must be between 1024 and 65535, got %d", c.Port)
	}
	return nil
}
