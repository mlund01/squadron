package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mlund01/squadron-sdk"
)

// tools holds the metadata for each tool provided by this plugin
var tools = map[string]*squadron.ToolInfo{
	"ping": {
		Name:        "ping",
		Description: "Returns 'pong' when called",
		Schema: squadron.Schema{
			Type:       squadron.TypeObject,
			Properties: squadron.PropertyMap{},
		},
	},
	"pong": {
		Name:        "pong",
		Description: "Returns 'ping' when called",
		Schema: squadron.Schema{
			Type:       squadron.TypeObject,
			Properties: squadron.PropertyMap{},
		},
	},
	"echo": {
		Name:        "echo",
		Description: "Echoes back the message provided",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"message": {
					Type:        squadron.TypeString,
					Description: "The message to echo back",
				},
				"all_caps": {
					Type:        squadron.TypeBoolean,
					Description: "When true, capitalizes the echoed message",
				},
			},
			Required: []string{"message"},
		},
	},
}

// PingerPlugin implements the ToolProvider interface
type PingerPlugin struct{}

// Configure applies settings (no-op for this plugin)
func (p *PingerPlugin) Configure(settings map[string]string) error {
	return nil
}

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

func (p *PingerPlugin) GetToolInfo(toolName string) (*squadron.ToolInfo, error) {
	info, ok := tools[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	return info, nil
}

func (p *PingerPlugin) ListTools() ([]*squadron.ToolInfo, error) {
	result := make([]*squadron.ToolInfo, 0, len(tools))
	for _, info := range tools {
		result = append(result, info)
	}
	return result, nil
}

func main() {
	squadron.Serve(&PingerPlugin{})
}
