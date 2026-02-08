package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"

	"squadron/aitools"
)

// Global plugin registry - plugins are shared across all config loads
var (
	globalRegistry     = make(map[string]*PluginClient) // key: "name:version"
	globalRegistryLock sync.RWMutex
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
	return filepath.Join(home, ".squadron", "plugins"), nil
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

// LoadPlugin loads a plugin by name and version.
// If source is provided and plugin not found locally, downloads from GitHub.
// Plugins are cached globally - if the same plugin was already loaded,
// the existing instance is returned. This allows browser sessions and
// other plugin state to persist across workflow tasks.
func LoadPlugin(name, version, source string) (*PluginClient, error) {
	key := name + ":" + version

	// Check if plugin is already loaded
	globalRegistryLock.RLock()
	if existing, ok := globalRegistry[key]; ok {
		globalRegistryLock.RUnlock()
		return existing, nil
	}
	globalRegistryLock.RUnlock()

	// Not found, need to load it
	globalRegistryLock.Lock()
	defer globalRegistryLock.Unlock()

	// Double-check after acquiring write lock
	if existing, ok := globalRegistry[key]; ok {
		return existing, nil
	}

	pluginPath, err := GetPluginPath(name, version)
	if err != nil {
		return nil, err
	}

	// If plugin doesn't exist locally, try to download
	if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
		if source == "" || version == "local" {
			return nil, fmt.Errorf("plugin not found: %s (version %s) at %s", name, version, pluginPath)
		}

		// Download from GitHub
		pluginDir, _ := GetPluginDir(name, version)
		fmt.Printf("Downloading plugin %s %s from %s...\n", name, version, source)
		if err := DownloadPlugin(source, version, pluginDir); err != nil {
			return nil, fmt.Errorf("failed to download plugin: %w", err)
		}
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

	// Connect and dispense the tool provider
	provider, err := DispenseToolProvider(client)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("failed to dispense plugin: %w", err)
	}

	pc := &PluginClient{
		client:   client,
		provider: provider,
		name:     name,
	}

	// Store in global registry
	globalRegistry[key] = pc

	return pc, nil
}

// Configure passes settings to the plugin
func (p *PluginClient) Configure(settings map[string]string) error {
	return p.provider.Configure(settings)
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

// Close shuts down the plugin.
// Note: When using globally cached plugins, prefer CloseAll() at program exit
// rather than closing individual plugins.
func (p *PluginClient) Close() {
	if p.client != nil {
		p.client.Kill()
	}
}

// CloseAll shuts down all globally cached plugins.
// Call this at program exit to clean up plugin processes.
func CloseAll() {
	globalRegistryLock.Lock()
	defer globalRegistryLock.Unlock()

	for key, pc := range globalRegistry {
		if pc.client != nil {
			pc.client.Kill()
		}
		delete(globalRegistry, key)
	}
}

// Name returns the plugin name
func (p *PluginClient) Name() string {
	return p.name
}
