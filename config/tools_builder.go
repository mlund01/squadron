package config

import (
	"strings"

	"squadron/aitools"
	squadronmcp "squadron/mcp"
	"squadron/plugin"
)

// BuildToolsMap creates a map of tool name -> Tool implementation from the agent's tools list
// Tools can be:
//   - Builtin tools: builtins.http.get, builtins.http.get
//   - Plugin tools: plugins.pinger.echo (external plugins)
//   - MCP tools: mcp.filesystem.read_file (consumer-side MCP servers)
//   - Custom tools: tools.weather, tools.shout (defined in HCL)
//
// datasetStore is optional and provides access to mission datasets for dataset tools.
// When datasetStore is provided (mission context), dataset tools are automatically injected.
func BuildToolsMap(agentTools []string, customTools []CustomTool, loadedPlugins map[string]*plugin.PluginClient, loadedMCPClients map[string]*squadronmcp.Client, datasetStore aitools.DatasetStore) map[string]aitools.Tool {
	tools := make(map[string]aitools.Tool)

	// Build a lookup map for custom tool definitions
	customToolMap := make(map[string]*CustomTool)
	for i := range customTools {
		customToolMap[customTools[i].Name] = &customTools[i]
	}

	// Add tools from the agent's tools list
	for _, toolRef := range agentTools {
		// Skip dataset tools in agent list - they're auto-injected when in mission context
		if strings.HasPrefix(toolRef, "builtins.dataset.") {
			continue
		}

		// Check for "builtins.{name}.all" - expand to all tools from that builtin namespace
		if strings.HasPrefix(toolRef, "builtins.") && strings.HasSuffix(toolRef, ".all") {
			parts := strings.Split(toolRef, ".")
			if len(parts) == 3 {
				namespaceName := parts[1]
				if builtinToolList, ok := BuiltinTools[namespaceName]; ok {
					for _, toolName := range builtinToolList {
						ref := "builtins." + namespaceName + "." + toolName
						tool := GetBuiltinTool(ref, datasetStore)
						if tool != nil {
							tools[ref] = tool
						}
					}
				}
			}
			continue
		}

		// Check for "plugins.{name}.all" - expand to all tools from an external plugin
		if strings.HasPrefix(toolRef, "plugins.") && strings.HasSuffix(toolRef, ".all") {
			parts := strings.Split(toolRef, ".")
			if len(parts) == 3 {
				pluginName := parts[1]
				if client, ok := loadedPlugins[pluginName]; ok {
					pluginTools, err := client.ListTools()
					if err == nil {
						for _, t := range pluginTools {
							ref := "plugins." + pluginName + "." + t.Name
							if tool, err := client.GetTool(t.Name); err == nil {
								tools[ref] = tool
							}
						}
					}
				}
			}
			continue
		}

		// Check for "mcp.{name}.all" - expand to all tools from a consumer MCP server
		if strings.HasPrefix(toolRef, "mcp.") && strings.HasSuffix(toolRef, ".all") {
			parts := strings.Split(toolRef, ".")
			if len(parts) == 3 {
				mcpName := parts[1]
				// A nil client means the MCP failed to load (e.g. OAuth
				// not yet authorized). Skip expansion — agents that
				// depended on these tools will run without them, and
				// any call attempt hits the tool-missing path downstream.
				if client, ok := loadedMCPClients[mcpName]; ok && client != nil {
					mcpTools, err := client.ListTools()
					if err == nil {
						for _, t := range mcpTools {
							ref := "mcp." + mcpName + "." + t.Name
							if tool, err := client.GetTool(t.Name); err == nil {
								tools[ref] = tool
							}
						}
					}
				}
			}
			continue
		}

		// Check if it's a builtin tool reference (builtins.{namespace}.{tool})
		if IsBuiltinTool(toolRef) {
			tool := GetBuiltinTool(toolRef, datasetStore)
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
		} else if strings.HasPrefix(toolRef, "mcp.") {
			// Consumer-side MCP tool
			parts := strings.Split(toolRef, ".")
			if len(parts) == 3 {
				mcpName := parts[1]
				toolName := parts[2]
				if client, ok := loadedMCPClients[mcpName]; ok && client != nil {
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

	// Auto-inject dataset tools when running in mission context
	if datasetStore != nil {
		tools["set_dataset"] = &aitools.SetDatasetTool{Store: datasetStore}
		tools["dataset_sample"] = &aitools.DatasetSampleTool{Store: datasetStore}
		tools["dataset_count"] = &aitools.DatasetCountTool{Store: datasetStore}
	}

	return tools
}

// GetBuiltinTool returns the aitools.Tool for a built-in tool reference
// datasetStore is optional and required for dataset tools.
func GetBuiltinTool(ref string, datasetStore aitools.DatasetStore) aitools.Tool {
	switch ref {
	case "builtins.http.get":
		return &aitools.HTTPGetTool{}
	case "builtins.http.post":
		return &aitools.HTTPPostTool{}
	case "builtins.http.put":
		return &aitools.HTTPPutTool{}
	case "builtins.http.patch":
		return &aitools.HTTPPatchTool{}
	case "builtins.http.delete":
		return &aitools.HTTPDeleteTool{}
	case "builtins.dataset.set":
		return &aitools.SetDatasetTool{Store: datasetStore}
	case "builtins.dataset.sample":
		return &aitools.DatasetSampleTool{Store: datasetStore}
	case "builtins.dataset.count":
		return &aitools.DatasetCountTool{Store: datasetStore}
	case "builtins.utils.sleep":
		return &aitools.SleepTool{}
	default:
		return nil
	}
}
