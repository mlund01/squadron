package mcphost

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerTools adds all squadron MCP tools to the server.
func registerTools(srv *server.MCPServer, h *handlers) {
	// Version & docs
	srv.AddTool(mcp.NewTool("list_version",
		mcp.WithDescription("Get the squadron version"),
	), h.listVersion)

	srv.AddTool(mcp.NewTool("list_doc_pages",
		mcp.WithDescription("List all available documentation pages"),
	), h.listDocPages)

	srv.AddTool(mcp.NewTool("get_doc_page",
		mcp.WithDescription("Get the contents of a documentation page"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the doc page (e.g. 'config/agents.md')")),
	), h.getDocPage)

	// Missions (config)
	srv.AddTool(mcp.NewTool("list_missions",
		mcp.WithDescription("List all available missions defined in config"),
	), h.listMissions)

	srv.AddTool(mcp.NewTool("get_mission_config",
		mcp.WithDescription("Get the full configuration of a mission"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Mission name")),
	), h.getMissionConfig)

	// Mission execution
	srv.AddTool(mcp.NewTool("run_mission",
		mcp.WithDescription("Start a mission. Returns immediately with a mission ID."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Mission name")),
		mcp.WithObject("inputs", mcp.Description("Optional mission inputs as key-value pairs")),
	), h.runMission)

	// Runs (history)
	srv.AddTool(mcp.NewTool("list_runs",
		mcp.WithDescription("List recent mission runs with optional filtering"),
		mcp.WithNumber("limit", mcp.Description("Max results to return (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Number of results to skip (default 0)")),
		mcp.WithString("mission_name", mcp.Description("Filter by mission name")),
	), h.listRuns)

	srv.AddTool(mcp.NewTool("get_run_details",
		mcp.WithDescription("Get high-level details of a mission run including task summaries and dataset stats"),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Mission run ID")),
	), h.getRunDetails)

	srv.AddTool(mcp.NewTool("get_run_config",
		mcp.WithDescription("Get the config snapshot that was used for a specific mission run"),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Mission run ID")),
	), h.getRunConfig)

	srv.AddTool(mcp.NewTool("get_run_task_details",
		mcp.WithDescription("Get detailed information about a specific task including subtasks and outputs"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
	), h.getRunTaskDetails)

	// Agents
	srv.AddTool(mcp.NewTool("list_agents",
		mcp.WithDescription("List all agents defined in config"),
	), h.listAgents)

	srv.AddTool(mcp.NewTool("get_agent_config",
		mcp.WithDescription("Get the full configuration of an agent"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Agent name")),
	), h.getAgentConfig)

	// Plugins
	srv.AddTool(mcp.NewTool("list_plugins",
		mcp.WithDescription("List all plugins defined in config"),
	), h.listPlugins)

	srv.AddTool(mcp.NewTool("list_plugin_tools",
		mcp.WithDescription("List all tools provided by a plugin"),
		mcp.WithString("plugin_name", mcp.Required(), mcp.Description("Plugin name")),
	), h.listPluginTools)

	srv.AddTool(mcp.NewTool("get_plugin_tool",
		mcp.WithDescription("Get detailed information about a specific plugin tool including its schema"),
		mcp.WithString("plugin_name", mcp.Required(), mcp.Description("Plugin name")),
		mcp.WithString("tool_name", mcp.Required(), mcp.Description("Tool name")),
	), h.getPluginTool)

	// Config operations
	srv.AddTool(mcp.NewTool("verify_config",
		mcp.WithDescription("Validate the current config files on disk without applying changes"),
	), h.verifyConfig)

	srv.AddTool(mcp.NewTool("reload_config",
		mcp.WithDescription("Re-read and validate config from disk, applying changes if valid"),
	), h.reloadConfig)

	// Variables
	srv.AddTool(mcp.NewTool("list_vars",
		mcp.WithDescription("List all configuration variables. Secret values are masked."),
	), h.listVars)

	srv.AddTool(mcp.NewTool("get_var",
		mcp.WithDescription("Get a single configuration variable. Secret values are masked."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Variable name")),
	), h.getVar)
}
