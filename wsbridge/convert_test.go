package wsbridge_test

import (
	"testing"

	"github.com/mlund01/squadron-wire/protocol"

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
				Name:    "local",
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

	byName := make(map[string]protocol.PluginInfo, len(ic.Plugins))
	for _, p := range ic.Plugins {
		byName[p.Name] = p
	}

	if got := byName["http"].Kind; got != "builtin" {
		t.Errorf("http.Kind = %q, want builtin", got)
	}
	if got := byName["utils"].Kind; got != "builtin" {
		t.Errorf("utils.Kind = %q, want builtin", got)
	}

	shell, ok := byName["shell"]
	if !ok {
		t.Fatal("shell plugin missing")
	}
	if shell.Kind != "plugin" {
		t.Errorf("shell.Kind = %q, want plugin", shell.Kind)
	}
	if shell.Version != "v0.0.2" {
		t.Errorf("shell.Version = %q, want v0.0.2", shell.Version)
	}

	fs, ok := byName["filesystem"]
	if !ok {
		t.Fatal("filesystem mcp missing")
	}
	if fs.Kind != "mcp" {
		t.Errorf("filesystem.Kind = %q, want mcp", fs.Kind)
	}
	if fs.Path != "npm:@modelcontextprotocol/server-filesystem" {
		t.Errorf("filesystem.Path = %q, want the npm source", fs.Path)
	}
	if fs.Version != "2026.1.14" {
		t.Errorf("filesystem.Version = %q, want 2026.1.14", fs.Version)
	}

	local, ok := byName["local"]
	if !ok {
		t.Fatal("local mcp missing")
	}
	if local.Kind != "mcp" {
		t.Errorf("local.Kind = %q, want mcp", local.Kind)
	}
	if local.Path != "/usr/local/bin/my-mcp" {
		t.Errorf("local.Path = %q, want the command", local.Path)
	}

	remote, ok := byName["remote"]
	if !ok {
		t.Fatal("remote mcp missing")
	}
	if remote.Kind != "mcp" {
		t.Errorf("remote.Kind = %q, want mcp", remote.Kind)
	}
	if remote.Path != "https://example.com/mcp" {
		t.Errorf("remote.Path = %q, want the url", remote.Path)
	}
}
