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
//
// datasetStore is optional and provides access to workflow datasets for dataset tools.
// When datasetStore is provided (workflow context), dataset tools are automatically injected.
func BuildToolsMap(agentTools []string, customTools []CustomTool, loadedPlugins map[string]*plugin.PluginClient, datasetStore aitools.DatasetStore) map[string]aitools.Tool {
	tools := make(map[string]aitools.Tool)

	// Build a lookup map for custom tool definitions
	customToolMap := make(map[string]*CustomTool)
	for i := range customTools {
		customToolMap[customTools[i].Name] = &customTools[i]
	}

	// Add tools from the agent's tools list
	for _, toolRef := range agentTools {
		// Skip dataset tools in agent list - they're auto-injected when in workflow context
		if strings.HasPrefix(toolRef, "plugins.dataset.") {
			continue
		}

		// Check for "plugins.{name}.all" - expand to all tools from that plugin
		if strings.HasPrefix(toolRef, "plugins.") && strings.HasSuffix(toolRef, ".all") {
			parts := strings.Split(toolRef, ".")
			if len(parts) == 3 {
				pluginName := parts[1]
				// Check internal plugins first
				if internalTools, ok := InternalPluginTools[pluginName]; ok {
					for _, toolName := range internalTools {
						ref := "plugins." + pluginName + "." + toolName
						tool := GetInternalPluginTool(ref, datasetStore)
						if tool != nil {
							tools[ref] = tool
						}
					}
				} else if client, ok := loadedPlugins[pluginName]; ok {
					// External plugin - get all tools
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

		// Check if it's a plugin tool reference (plugins.{namespace}.{tool})
		if IsInternalPluginTool(toolRef) {
			// Internal plugin tool (bash, http)
			tool := GetInternalPluginTool(toolRef, datasetStore)
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

	// Auto-inject dataset tools when running in workflow context
	if datasetStore != nil {
		tools["set_dataset"] = &aitools.SetDatasetTool{Store: datasetStore}
		tools["dataset_sample"] = &aitools.DatasetSampleTool{Store: datasetStore}
		tools["dataset_count"] = &aitools.DatasetCountTool{Store: datasetStore}
	}

	return tools
}

// GetInternalPluginTool returns the aitools.Tool for an internal plugin tool reference
// datasetStore is optional and required for dataset tools.
func GetInternalPluginTool(ref string, datasetStore aitools.DatasetStore) aitools.Tool {
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
	case "plugins.dataset.set":
		return &aitools.SetDatasetTool{Store: datasetStore}
	case "plugins.dataset.sample":
		return &aitools.DatasetSampleTool{Store: datasetStore}
	case "plugins.dataset.count":
		return &aitools.DatasetCountTool{Store: datasetStore}
	default:
		return nil
	}
}
