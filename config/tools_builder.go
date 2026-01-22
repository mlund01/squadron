package config

import (
	"strings"

	"squad/aitools"
	"squad/plugin"
)

// BuildToolsMap creates a map of tool name -> Tool implementation from the agent's tools list
// Tools can be:
//   - Plugin tools: plugins.bash.bash, plugins.http.get, plugins.pinger.echo
//   - Custom tools: tools.weather, tools.shout (defined in HCL)
func BuildToolsMap(agentTools []string, customTools []CustomTool, loadedPlugins map[string]*plugin.PluginClient) map[string]aitools.Tool {
	tools := make(map[string]aitools.Tool)

	// Build a lookup map for custom tool definitions
	customToolMap := make(map[string]*CustomTool)
	for i := range customTools {
		customToolMap[customTools[i].Name] = &customTools[i]
	}

	// Add tools from the agent's tools list
	for _, toolRef := range agentTools {
		// Check if it's a plugin tool reference (plugins.{namespace}.{tool})
		if IsInternalPluginTool(toolRef) {
			// Internal plugin tool (bash, http)
			tool := GetInternalPluginTool(toolRef)
			if tool != nil {
				tools[toolRef] = tool
			}
		} else if strings.HasPrefix(toolRef, "plugins.") {
			// External plugin tool
			parts := strings.Split(toolRef, ".")
			if len(parts) == 3 {
				pluginName := parts[1]
				toolName := parts[2]
				if client, ok := loadedPlugins[pluginName]; ok {
					if tool, err := client.GetTool(toolName); err == nil {
						tools[toolRef] = tool
					}
				}
			}
		} else if strings.HasPrefix(toolRef, "tools.") {
			// Custom tool reference (tools.weather)
			customToolName := strings.TrimPrefix(toolRef, "tools.")
			if ct, ok := customToolMap[customToolName]; ok {
				tool := ct.ToToolWithPlugins(loadedPlugins)
				if tool != nil {
					tools[toolRef] = tool
				}
			}
		} else if ct, ok := customToolMap[toolRef]; ok {
			// Legacy: bare custom tool name
			tool := ct.ToToolWithPlugins(loadedPlugins)
			if tool != nil {
				tools[toolRef] = tool
			}
		}
	}

	return tools
}

// GetInternalPluginTool returns the aitools.Tool for an internal plugin tool reference
func GetInternalPluginTool(ref string) aitools.Tool {
	switch ref {
	case "plugins.bash.bash":
		return &aitools.BashTool{}
	case "plugins.http.get":
		return &aitools.HTTPGetTool{}
	case "plugins.http.post":
		return &aitools.HTTPPostTool{}
	case "plugins.http.put":
		return &aitools.HTTPPutTool{}
	case "plugins.http.patch":
		return &aitools.HTTPPatchTool{}
	case "plugins.http.delete":
		return &aitools.HTTPDeleteTool{}
	default:
		return nil
	}
}
