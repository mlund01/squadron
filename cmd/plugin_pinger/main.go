package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/go-plugin"

	"squad/aitools"
	squadplugin "squad/plugin"
)

// tools holds the metadata for each tool provided by this plugin
var tools = map[string]*squadplugin.ToolInfo{
	"ping": {
		Name:        "ping",
		Description: "Returns 'pong' when called",
		Schema: aitools.Schema{
			Type:       aitools.TypeObject,
			Properties: aitools.PropertyMap{},
		},
	},
	"pong": {
		Name:        "pong",
		Description: "Returns 'ping' when called",
		Schema: aitools.Schema{
			Type:       aitools.TypeObject,
			Properties: aitools.PropertyMap{},
		},
	},
	"echo": {
		Name:        "echo",
		Description: "Echoes back the message provided",
		Schema: aitools.Schema{
			Type: aitools.TypeObject,
			Properties: aitools.PropertyMap{
				"message": {
					Type:        aitools.TypeString,
					Description: "The message to echo back",
				},
				"all_caps": {
					Type:        aitools.TypeBoolean,
					Description: "When true, capitalizes the echoed message",
				},
			},
			Required: []string{"message"},
		},
	},
}

// PingerPlugin implements the ToolProvider interface
type PingerPlugin struct{}

func (p *PingerPlugin) Call(toolName string, payload string) (string, error) {
	switch toolName {
	case "ping":
		return "pong", nil
	case "pong":
		return "ping", nil
	case "echo":
		var params struct {
			Message string `json:"message"`
			AllCaps bool   `json:"all_caps"`
		}
		if err := json.Unmarshal([]byte(payload), &params); err != nil {
			return "", fmt.Errorf("invalid payload: %w", err)
		}
		if params.AllCaps {
			return strings.ToUpper(params.Message), nil
		}
		return params.Message, nil
	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}

func (p *PingerPlugin) GetToolInfo(toolName string) (*squadplugin.ToolInfo, error) {
	info, ok := tools[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	return info, nil
}

func (p *PingerPlugin) ListTools() ([]*squadplugin.ToolInfo, error) {
	result := make([]*squadplugin.ToolInfo, 0, len(tools))
	for _, info := range tools {
		result = append(result, info)
	}
	return result, nil
}

func main() {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: squadplugin.Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &squadplugin.ToolPluginGRPCPlugin{Impl: &PingerPlugin{}},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
