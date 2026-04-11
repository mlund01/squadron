package wsbridge_test

import (
	"testing"

	"squadron/config"
	"squadron/wsbridge"
)

// TestConfigToInstanceConfig_PluginKinds verifies that plugins, builtins, and
// MCP servers all land in the InstanceConfig.Plugins slice with their Kind
// field correctly populated so the command center can render them as
// separately categorized chips on the tools page.
func TestConfigToInstanceConfig_PluginKinds(t *testing.T) {
	cfg := &config.Config{
		Plugins: []config.Plugin{
			{Name: "shell", Source: "github.com/mlund01/plugin_shell", Version: "v0.0.2"},
		},
		MCPServers: []config.MCPServer{
			{
				Name:    "filesystem",
				Source:  "npm:@modelcontextprotocol/server-filesystem",
				Version: "2026.1.14",
			},
			{
				Name: "local",
				// bare command mode — no version, no source
				Command: "/usr/local/bin/my-mcp",
				Args:    []string{"--debug"},
			},
			{
				Name: "remote",
				URL:  "https://example.com/mcp",
			},
		},
	}

	ic := wsbridge.ConfigToInstanceConfig(cfg)

	byName := make(map[string]struct {
		Kind    string
		Path    string
		Version string
	})
	for _, p := range ic.Plugins {
		byName[p.Name] = struct {
			Kind    string
			Path    string
			Version string
		}{p.Kind, p.Path, p.Version}
	}

	// Builtins should be present and marked as "builtin".
	if got := byName["http"].Kind; got != "builtin" {
		t.Errorf("http plugin Kind = %q, want \"builtin\"", got)
	}
	if got := byName["utils"].Kind; got != "builtin" {
		t.Errorf("utils plugin Kind = %q, want \"builtin\"", got)
	}

	// Native plugin.
	shell, ok := byName["shell"]
	if !ok {
		t.Fatal("shell plugin missing from InstanceConfig.Plugins")
	}
	if shell.Kind != "plugin" {
		t.Errorf("shell Kind = %q, want \"plugin\"", shell.Kind)
	}
	if shell.Version != "v0.0.2" {
		t.Errorf("shell Version = %q, want v0.0.2", shell.Version)
	}

	// MCP source-backed server: Path should carry the source and Version
	// should be populated.
	fs, ok := byName["filesystem"]
	if !ok {
		t.Fatal("filesystem mcp missing")
	}
	if fs.Kind != "mcp" {
		t.Errorf("filesystem Kind = %q, want \"mcp\"", fs.Kind)
	}
	if fs.Path != "npm:@modelcontextprotocol/server-filesystem" {
		t.Errorf("filesystem Path = %q, want the npm source", fs.Path)
	}
	if fs.Version != "2026.1.14" {
		t.Errorf("filesystem Version = %q, want 2026.1.14", fs.Version)
	}

	// MCP bare command: Path should fall back to the command.
	local, ok := byName["local"]
	if !ok {
		t.Fatal("local mcp missing")
	}
	if local.Kind != "mcp" {
		t.Errorf("local Kind = %q, want \"mcp\"", local.Kind)
	}
	if local.Path != "/usr/local/bin/my-mcp" {
		t.Errorf("local Path = %q, want the command", local.Path)
	}

	// MCP remote url: Path should fall back to the url.
	remote, ok := byName["remote"]
	if !ok {
		t.Fatal("remote mcp missing")
	}
	if remote.Kind != "mcp" {
		t.Errorf("remote Kind = %q, want \"mcp\"", remote.Kind)
	}
	if remote.Path != "https://example.com/mcp" {
		t.Errorf("remote Path = %q, want the url", remote.Path)
	}
}

// TestConfigToInstanceConfig_NoMCP verifies nothing breaks when MCPServers is
// empty and the host doesn't run any MCP subprocesses.
func TestConfigToInstanceConfig_NoMCP(t *testing.T) {
	cfg := &config.Config{}

	ic := wsbridge.ConfigToInstanceConfig(cfg)

	// Should still contain builtin plugin entries.
	foundBuiltin := false
	for _, p := range ic.Plugins {
		if p.Kind == "mcp" {
			t.Errorf("found unexpected mcp plugin: %+v", p)
		}
		if p.Kind == "builtin" {
			foundBuiltin = true
		}
	}
	if !foundBuiltin {
		t.Error("no builtin plugins in InstanceConfig — builtins should always be present")
	}
}
