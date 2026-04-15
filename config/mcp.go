package config

import (
	"fmt"
	"strings"
)

// MCPHostConfig defines settings for the built-in MCP host (Squadron acting
// as an MCP server so other LLMs can consume its tools). Previously the
// `mcp` singleton block; renamed to `mcp_host` to free up `mcp` for the
// labeled consumer-side block (`mcp "name" { ... }`).
//
// If no mcp_host block is present in config, the MCP host is not started.
type MCPHostConfig struct {
	Enabled bool   `hcl:"enabled,optional"`
	Port    int    `hcl:"port,optional"`
	Secret  string `hcl:"secret,optional"`
}

// Defaults fills in default values for unset fields.
func (c *MCPHostConfig) Defaults() {
	if c.Port <= 0 {
		c.Port = 8090
	}
}

// Validate checks that the host config is valid.
func (c *MCPHostConfig) Validate() error {
	if c.Port < 1024 || c.Port > 65535 {
		return fmt.Errorf("mcp_host port must be between 1024 and 65535, got %d", c.Port)
	}
	return nil
}

// MCPServer represents one consumer-side `mcp "name" { ... }` block.
// Exactly one of Command, URL, or Source must be set.
type MCPServer struct {
	Name string

	// Bare-command mode (no auto-install).
	Command string

	// HTTP transport mode.
	URL     string
	Headers map[string]string

	// Auto-install mode: "npm:<pkg>" or "github.com/<owner>/<repo>".
	Source  string
	Version string
	Entry   string // github source only

	// OAuth fields (HTTP-only, for servers that don't support DCR).
	ClientID     string
	ClientSecret string

	// Common fields.
	Args []string
	Env  map[string]string
}

// Location returns the human-readable "what does this server point at"
// string used by `squadron mcp status` and `squadron verify`. For HTTP
// servers it is the URL; for stdio servers it is the source or command.
// Never wrapped in parens — callers compose it directly into their output.
func (m *MCPServer) Location() string {
	switch {
	case m.URL != "":
		return m.URL
	case m.Source != "":
		return m.Source
	case m.Command != "":
		return m.Command
	default:
		return "(unknown)"
	}
}

// Validate enforces the cross-field rules for an MCPServer block.
func (m *MCPServer) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("mcp server name is required")
	}
	if IsReservedBuiltinNamespace(m.Name) {
		return fmt.Errorf("mcp server name '%s' is reserved", m.Name)
	}

	// Exactly one of command / url / source.
	modes := 0
	if m.Command != "" {
		modes++
	}
	if m.URL != "" {
		modes++
	}
	if m.Source != "" {
		modes++
	}
	switch modes {
	case 0:
		return fmt.Errorf("mcp '%s': one of command, url, or source is required", m.Name)
	case 1:
		// ok
	default:
		return fmt.Errorf("mcp '%s': command, url, and source are mutually exclusive", m.Name)
	}

	// Transport-gated fields.
	if m.URL != "" {
		if len(m.Env) > 0 {
			return fmt.Errorf("mcp '%s': env is not valid on http (url) servers", m.Name)
		}
		if len(m.Args) > 0 {
			return fmt.Errorf("mcp '%s': args is not valid on http (url) servers", m.Name)
		}
	} else {
		if len(m.Headers) > 0 {
			return fmt.Errorf("mcp '%s': headers is only valid on http (url) servers", m.Name)
		}
		if m.ClientID != "" {
			return fmt.Errorf("mcp '%s': client_id is only valid on http (url) servers", m.Name)
		}
	}

	// client_secret without client_id is meaningless.
	if m.ClientSecret != "" && m.ClientID == "" {
		return fmt.Errorf("mcp '%s': client_secret requires client_id", m.Name)
	}

	// Version rules.
	if m.Source != "" {
		if m.Version == "" {
			return fmt.Errorf("mcp '%s': version is required when source is set", m.Name)
		}
	} else {
		if m.Version != "" {
			return fmt.Errorf("mcp '%s': version is only valid with source", m.Name)
		}
	}

	// Source scheme and entry rules.
	if m.Source != "" {
		isNPM := strings.HasPrefix(m.Source, "npm:")
		isGitHub := strings.HasPrefix(m.Source, "github.com/")
		if !isNPM && !isGitHub {
			return fmt.Errorf("mcp '%s': source must start with 'npm:' or 'github.com/', got %q", m.Name, m.Source)
		}
		if isNPM {
			pkg := strings.TrimPrefix(m.Source, "npm:")
			if strings.TrimSpace(pkg) == "" {
				return fmt.Errorf("mcp '%s': source 'npm:' requires a package name", m.Name)
			}
		}
		if isGitHub {
			rest := strings.TrimPrefix(m.Source, "github.com/")
			parts := strings.Split(rest, "/")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("mcp '%s': source %q must be of the form github.com/owner/repo", m.Name, m.Source)
			}
		}
		if m.Entry != "" && !isGitHub {
			return fmt.Errorf("mcp '%s': entry is only valid with github sources", m.Name)
		}
	} else if m.Entry != "" {
		return fmt.Errorf("mcp '%s': entry is only valid with source = \"github.com/...\"", m.Name)
	}

	return nil
}
