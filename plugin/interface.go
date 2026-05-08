package plugin

import (
	"context"
	"encoding/json"

	goplugin "github.com/hashicorp/go-plugin"

	"squadron/aitools"
	"github.com/mlund01/squadron-sdk"
)

var (
	Handshake = squadron.Handshake
	PluginMap = squadron.PluginMap
)

type ToolInfo struct {
	Name         string
	Description  string
	Schema       aitools.Schema
	OutputSchema json.RawMessage
}

type ToolProvider interface {
	Configure(settings map[string]string) error
	Call(ctx context.Context, toolName string, payload string) (string, error)
	GetToolInfo(toolName string) (*ToolInfo, error)
	ListTools() ([]*ToolInfo, error)
}

type sdkProviderWrapper struct {
	impl squadron.ToolProvider
}

func (w *sdkProviderWrapper) Configure(settings map[string]string) error {
	return w.impl.Configure(settings)
}

func (w *sdkProviderWrapper) Call(ctx context.Context, toolName string, payload string) (string, error) {
	return w.impl.Call(ctx, toolName, payload)
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

func sdkToLocalToolInfo(t *squadron.ToolInfo) *ToolInfo {
	raw := t.RawSchema
	if len(raw) == 0 {
		marshaled, _ := json.Marshal(t.Schema)
		raw = marshaled
	}

	var schema aitools.Schema
	_ = json.Unmarshal(raw, &schema)
	schema = schema.WithRawJSONSchema(raw)

	return &ToolInfo{
		Name:         t.Name,
		Description:  t.Description,
		Schema:       schema,
		OutputSchema: t.OutputSchema,
	}
}

func WrapSDKProvider(impl squadron.ToolProvider) ToolProvider {
	return &sdkProviderWrapper{impl: impl}
}

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
