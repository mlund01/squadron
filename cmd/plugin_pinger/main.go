package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mlund01/squad-sdk"
)

// tools holds the metadata for each tool provided by this plugin
var tools = map[string]*squad.ToolInfo{
	"ping": {
		Name:        "ping",
		Description: "Returns 'pong' when called",
		Schema: squad.Schema{
			Type:       squad.TypeObject,
			Properties: squad.PropertyMap{},
		},
	},
	"pong": {
		Name:        "pong",
		Description: "Returns 'ping' when called",
		Schema: squad.Schema{
			Type:       squad.TypeObject,
			Properties: squad.PropertyMap{},
		},
	},
	"echo": {
		Name:        "echo",
		Description: "Echoes back the message provided",
		Schema: squad.Schema{
			Type: squad.TypeObject,
			Properties: squad.PropertyMap{
				"message": {
					Type:        squad.TypeString,
					Description: "The message to echo back",
				},
				"all_caps": {
					Type:        squad.TypeBoolean,
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

func (p *PingerPlugin) GetToolInfo(toolName string) (*squad.ToolInfo, error) {
	info, ok := tools[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	return info, nil
}

func (p *PingerPlugin) ListTools() ([]*squad.ToolInfo, error) {
	result := make([]*squad.ToolInfo, 0, len(tools))
	for _, info := range tools {
		result = append(result, info)
	}
	return result, nil
}

func main() {
	squad.Serve(&PingerPlugin{})
}
