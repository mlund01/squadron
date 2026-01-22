package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"

	"squad/aitools"
)

// PluginClient wraps a go-plugin client and provides access to the tool plugin
type PluginClient struct {
	client   *plugin.Client
	provider ToolProvider
	name     string
}

// GetPluginsDir returns the base directory for plugins
func GetPluginsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".squad", "plugins"), nil
}

// GetPluginPath returns the path to a plugin executable
func GetPluginPath(name, version string) (string, error) {
	pluginsDir, err := GetPluginsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(pluginsDir, name, version, "plugin"), nil
}

// GetPluginDir returns the directory for a specific plugin version
func GetPluginDir(name, version string) (string, error) {
	pluginsDir, err := GetPluginsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(pluginsDir, name, version), nil
}

// LoadPlugin loads a plugin by name and version
func LoadPlugin(name, version string) (*PluginClient, error) {
	pluginPath, err := GetPluginPath(name, version)
	if err != nil {
		return nil, err
	}

	// Check if plugin exists
	if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("plugin not found: %s (version %s) at %s", name, version, pluginPath)
	}

	// Create a logger that discards output (or you can configure it)
	logger := hclog.New(&hclog.LoggerOptions{
		Name:   "plugin",
		Output: os.Stderr,
		Level:  hclog.Error, // Only show errors
	})

	// Create the plugin client with gRPC
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  Handshake,
		Plugins:          PluginMap,
		Cmd:              exec.Command(pluginPath),
		Logger:           logger,
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
	})

	// Connect via gRPC
	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("failed to connect to plugin: %w", err)
	}

	// Request the plugin
	raw, err := rpcClient.Dispense("tool")
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("failed to dispense plugin: %w", err)
	}

	provider, ok := raw.(ToolProvider)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("plugin does not implement ToolProvider interface")
	}

	return &PluginClient{
		client:   client,
		provider: provider,
		name:     name,
	}, nil
}

// Call invokes a tool on the plugin
func (p *PluginClient) Call(toolName string, payload string) (string, error) {
	return p.provider.Call(toolName, payload)
}

// GetToolInfo returns metadata about a specific tool
func (p *PluginClient) GetToolInfo(toolName string) (*ToolInfo, error) {
	return p.provider.GetToolInfo(toolName)
}

// ListTools returns info for all tools this plugin provides
func (p *PluginClient) ListTools() ([]*ToolInfo, error) {
	return p.provider.ListTools()
}

// GetTool returns an aitools.Tool implementation for the specified tool
func (p *PluginClient) GetTool(toolName string) (aitools.Tool, error) {
	info, err := p.provider.GetToolInfo(toolName)
	if err != nil {
		return nil, err
	}
	return NewPluginTool(p.provider, info), nil
}

// GetAllTools returns a map of all tools provided by this plugin
func (p *PluginClient) GetAllTools() (map[string]aitools.Tool, error) {
	infos, err := p.provider.ListTools()
	if err != nil {
		return nil, err
	}
	tools := make(map[string]aitools.Tool, len(infos))
	for _, info := range infos {
		tools[info.Name] = NewPluginTool(p.provider, info)
	}
	return tools, nil
}

// Close shuts down the plugin
func (p *PluginClient) Close() {
	if p.client != nil {
		p.client.Kill()
	}
}

// Name returns the plugin name
func (p *PluginClient) Name() string {
	return p.name
}
