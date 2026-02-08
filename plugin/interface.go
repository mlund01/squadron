package plugin

import (
	"encoding/json"

	goplugin "github.com/hashicorp/go-plugin"

	"squadron/aitools"
	"github.com/mlund01/squadron-sdk"
)

// Re-export SDK types for convenience
var (
	// Handshake is the handshake config for plugins
	Handshake = squadron.Handshake

	// PluginMap is the map of plugins we can dispense
	PluginMap = squadron.PluginMap
)

// ToolInfo contains metadata about a tool (with aitools.Schema for internal use)
type ToolInfo struct {
	Name        string
	Description string
	Schema      aitools.Schema
}

// ToolProvider wraps squadron.ToolProvider to convert schema types
type ToolProvider interface {
	// Configure passes settings from HCL config to the plugin
	Configure(settings map[string]string) error

	// Call invokes a tool with the given JSON payload
	Call(toolName string, payload string) (string, error)

	// GetToolInfo returns metadata about a specific tool
	GetToolInfo(toolName string) (*ToolInfo, error)

	// ListTools returns info for all tools this plugin provides
	ListTools() ([]*ToolInfo, error)
}

// sdkProviderWrapper wraps squadron.ToolProvider to implement our ToolProvider
type sdkProviderWrapper struct {
	impl squadron.ToolProvider
}

func (w *sdkProviderWrapper) Configure(settings map[string]string) error {
	return w.impl.Configure(settings)
}

func (w *sdkProviderWrapper) Call(toolName string, payload string) (string, error) {
	return w.impl.Call(toolName, payload)
}

func (w *sdkProviderWrapper) GetToolInfo(toolName string) (*ToolInfo, error) {
	sdkInfo, err := w.impl.GetToolInfo(toolName)
	if err != nil {
		return nil, err
	}
	return sdkToLocalToolInfo(sdkInfo), nil
}

func (w *sdkProviderWrapper) ListTools() ([]*ToolInfo, error) {
	sdkTools, err := w.impl.ListTools()
	if err != nil {
		return nil, err
	}
	tools := make([]*ToolInfo, len(sdkTools))
	for i, t := range sdkTools {
		tools[i] = sdkToLocalToolInfo(t)
	}
	return tools, nil
}

// sdkToLocalToolInfo converts squadron.ToolInfo to local ToolInfo with aitools.Schema
func sdkToLocalToolInfo(t *squadron.ToolInfo) *ToolInfo {
	// Convert squadron.Schema to aitools.Schema via JSON (same structure)
	var schema aitools.Schema
	schemaJSON, _ := json.Marshal(t.Schema)
	json.Unmarshal(schemaJSON, &schema)

	return &ToolInfo{
		Name:        t.Name,
		Description: t.Description,
		Schema:      schema,
	}
}

// WrapSDKProvider wraps an squadron.ToolProvider to implement our ToolProvider interface
func WrapSDKProvider(impl squadron.ToolProvider) ToolProvider {
	return &sdkProviderWrapper{impl: impl}
}

// DispenseToolProvider gets a ToolProvider from a plugin client
func DispenseToolProvider(client *goplugin.Client) (ToolProvider, error) {
	rpcClient, err := client.Client()
	if err != nil {
		return nil, err
	}

	raw, err := rpcClient.Dispense("tool")
	if err != nil {
		return nil, err
	}

	sdkProvider, ok := raw.(squadron.ToolProvider)
	if !ok {
		return nil, err
	}

	return WrapSDKProvider(sdkProvider), nil
}
