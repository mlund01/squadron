package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"squadron/config"
	"squadron/docs"
	"squadron/store"
)

// handlers implements all MCP tool handlers.
type handlers struct {
	deps Deps
}

// =============================================================================
// Version & Documentation
// =============================================================================

func (h *handlers) listVersion(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return toolResult(map[string]string{
		"version": h.deps.Version,
	})
}

func (h *handlers) listDocPages(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var pages []string
	err := fs.WalkDir(docs.DocsFS, "content", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".mdx") {
			pages = append(pages, strings.TrimPrefix(path, "content/"))
		}
		return nil
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list doc pages: %v", err)), nil
	}
	return toolResult(map[string]any{
		"pages": pages,
	})
}

func (h *handlers) getDocPage(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := req.GetArguments()["path"].(string)
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	// Prevent directory traversal
	if strings.Contains(path, "..") {
		return mcp.NewToolResultError("invalid path"), nil
	}

	content, err := fs.ReadFile(docs.DocsFS, "content/"+path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("page not found: %s", path)), nil
	}

	return toolResult(map[string]string{
		"path":    path,
		"content": string(content),
	})
}

// =============================================================================
// Missions (config)
// =============================================================================

func (h *handlers) listMissions(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := h.deps.Config()
	if cfg == nil {
		return mcp.NewToolResultError("config not loaded"), nil
	}

	type missionSummary struct {
		Name      string   `json:"name"`
		Directive string   `json:"directive,omitempty"`
		Agents    []string `json:"agents"`
		TaskCount int      `json:"taskCount"`
	}

	missions := make([]missionSummary, 0, len(cfg.Missions))
	for _, m := range cfg.Missions {
		missions = append(missions, missionSummary{
			Name:      m.Name,
			Directive: m.Directive,
			Agents:    m.Agents,
			TaskCount: len(m.Tasks),
		})
	}

	return toolResult(map[string]any{
		"missions": missions,
	})
}

func (h *handlers) getMissionConfig(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, _ := req.GetArguments()["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	cfg := h.deps.Config()
	if cfg == nil {
		return mcp.NewToolResultError("config not loaded"), nil
	}

	for _, m := range cfg.Missions {
		if m.Name == name {
			return toolResult(map[string]any{
				"name":   m.Name,
				"config": m,
			})
		}
	}

	return mcp.NewToolResultError(fmt.Sprintf("mission %q not found", name)), nil
}

// =============================================================================
// Mission Execution
// =============================================================================

func (h *handlers) runMission(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, _ := req.GetArguments()["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	// Parse optional inputs
	inputs := make(map[string]string)
	if rawInputs, ok := req.GetArguments()["inputs"]; ok && rawInputs != nil {
		if inputMap, ok := rawInputs.(map[string]any); ok {
			for k, v := range inputMap {
				inputs[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	if h.deps.RunMission == nil {
		return mcp.NewToolResultError("mission execution not available"), nil
	}

	missionID, err := h.deps.RunMission(name, inputs)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to start mission: %v", err)), nil
	}

	return toolResult(map[string]any{
		"accepted":  true,
		"missionId": missionID,
	})
}

// =============================================================================
// Runs (history)
// =============================================================================

func (h *handlers) listRuns(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	limit := 20
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	offset := 0
	if v, ok := args["offset"].(float64); ok && v >= 0 {
		offset = int(v)
	}
	missionFilter, _ := args["mission_name"].(string)

	records, total, err := h.deps.Stores.Missions.ListMissions(limit, offset)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list runs: %v", err)), nil
	}

	// Post-filter by mission name if requested
	if missionFilter != "" {
		filtered := records[:0]
		for _, r := range records {
			if r.MissionName == missionFilter {
				filtered = append(filtered, r)
			}
		}
		records = filtered
	}

	type runSummary struct {
		ID          string     `json:"id"`
		MissionName string     `json:"missionName"`
		Status      string     `json:"status"`
		StartedAt   any        `json:"startedAt"`
		FinishedAt  any        `json:"finishedAt,omitempty"`
		Inputs      any        `json:"inputs,omitempty"`
		TaskCount   int        `json:"taskCount"`
		Datasets    []datasetSummary `json:"datasets,omitempty"`
	}

	runs := make([]runSummary, 0, len(records))
	for _, r := range records {
		tasks, _ := h.deps.Stores.Missions.GetTasksByMission(r.ID)
		datasets, _ := h.deps.Stores.Datasets.ListDatasets(r.ID)

		var inputs any
		if r.InputValuesJSON != "" && r.InputValuesJSON != "{}" {
			json.Unmarshal([]byte(r.InputValuesJSON), &inputs)
		}

		runs = append(runs, runSummary{
			ID:          r.ID,
			MissionName: r.MissionName,
			Status:      r.Status,
			StartedAt:   r.StartedAt,
			FinishedAt:  r.FinishedAt,
			Inputs:      inputs,
			TaskCount:   len(tasks),
			Datasets:    toDatasetSummaries(datasets),
		})
	}

	return toolResult(map[string]any{
		"runs":  runs,
		"total": total,
	})
}

type datasetSummary struct {
	Name      string `json:"name"`
	ItemCount int    `json:"itemCount"`
}

func toDatasetSummaries(datasets []store.DatasetInfo) []datasetSummary {
	if len(datasets) == 0 {
		return nil
	}
	out := make([]datasetSummary, len(datasets))
	for i, d := range datasets {
		out[i] = datasetSummary{Name: d.Name, ItemCount: d.ItemCount}
	}
	return out
}

func (h *handlers) getRunDetails(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	runID, _ := req.GetArguments()["run_id"].(string)
	if runID == "" {
		return mcp.NewToolResultError("run_id is required"), nil
	}

	record, err := h.deps.Stores.Missions.GetMission(runID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get run: %v", err)), nil
	}
	if record == nil {
		return mcp.NewToolResultError(fmt.Sprintf("run %q not found", runID)), nil
	}

	tasks, err := h.deps.Stores.Missions.GetTasksByMission(runID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get tasks: %v", err)), nil
	}

	datasets, _ := h.deps.Stores.Datasets.ListDatasets(runID)

	var inputs any
	if record.InputValuesJSON != "" && record.InputValuesJSON != "{}" {
		json.Unmarshal([]byte(record.InputValuesJSON), &inputs)
	}

	type taskSummary struct {
		ID         string  `json:"id"`
		TaskName   string  `json:"taskName"`
		Status     string  `json:"status"`
		StartedAt  any     `json:"startedAt,omitempty"`
		FinishedAt any     `json:"finishedAt,omitempty"`
		Error      *string `json:"error,omitempty"`
	}

	taskSummaries := make([]taskSummary, 0, len(tasks))
	for _, t := range tasks {
		taskSummaries = append(taskSummaries, taskSummary{
			ID:         t.ID,
			TaskName:   t.TaskName,
			Status:     t.Status,
			StartedAt:  t.StartedAt,
			FinishedAt: t.FinishedAt,
			Error:      t.Error,
		})
	}

	return toolResult(map[string]any{
		"id":          record.ID,
		"missionName": record.MissionName,
		"status":      record.Status,
		"startedAt":   record.StartedAt,
		"finishedAt":  record.FinishedAt,
		"inputs":      inputs,
		"tasks":       taskSummaries,
		"datasets":    toDatasetSummaries(datasets),
	})
}

func (h *handlers) getRunConfig(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	runID, _ := req.GetArguments()["run_id"].(string)
	if runID == "" {
		return mcp.NewToolResultError("run_id is required"), nil
	}

	record, err := h.deps.Stores.Missions.GetMission(runID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get run: %v", err)), nil
	}
	if record == nil {
		return mcp.NewToolResultError(fmt.Sprintf("run %q not found", runID)), nil
	}

	var config any
	if record.ConfigJSON != "" {
		json.Unmarshal([]byte(record.ConfigJSON), &config)
	}

	return toolResult(map[string]any{
		"id":          record.ID,
		"missionName": record.MissionName,
		"config":      config,
	})
}

func (h *handlers) getRunTaskDetails(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, _ := req.GetArguments()["task_id"].(string)
	if taskID == "" {
		return mcp.NewToolResultError("task_id is required"), nil
	}

	task, err := h.deps.Stores.Missions.GetTask(taskID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get task: %v", err)), nil
	}
	if task == nil {
		return mcp.NewToolResultError(fmt.Sprintf("task %q not found", taskID)), nil
	}

	subtasks, err := h.deps.Stores.Missions.GetSubtasksByTask(taskID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get subtasks: %v", err)), nil
	}

	outputs, err := h.deps.Stores.Missions.GetTaskOutputs(taskID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get outputs: %v", err)), nil
	}

	inputs, err := h.deps.Stores.Missions.GetTaskInputs(taskID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get inputs: %v", err)), nil
	}

	// Parse output JSON if present
	var output any
	if task.OutputJSON != nil && *task.OutputJSON != "" {
		json.Unmarshal([]byte(*task.OutputJSON), &output)
	}

	return toolResult(map[string]any{
		"task":     task,
		"output":   output,
		"subtasks": subtasks,
		"outputs":  outputs,
		"inputs":   inputs,
	})
}

// =============================================================================
// Agents
// =============================================================================

func (h *handlers) listAgents(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := h.deps.Config()
	if cfg == nil {
		return mcp.NewToolResultError("config not loaded"), nil
	}

	type agentSummary struct {
		Name  string   `json:"name"`
		Model string   `json:"model"`
		Role  string   `json:"role,omitempty"`
		Tools []string `json:"tools"`
	}

	agents := make([]agentSummary, 0, len(cfg.Agents))
	for _, a := range cfg.Agents {
		agents = append(agents, agentSummary{
			Name:  a.Name,
			Model: a.Model,
			Role:  a.Role,
			Tools: a.Tools,
		})
	}

	return toolResult(map[string]any{
		"agents": agents,
	})
}

func (h *handlers) getAgentConfig(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, _ := req.GetArguments()["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	cfg := h.deps.Config()
	if cfg == nil {
		return mcp.NewToolResultError("config not loaded"), nil
	}

	for _, a := range cfg.Agents {
		if a.Name == name {
			return toolResult(map[string]any{
				"name":   a.Name,
				"config": a,
			})
		}
	}

	return mcp.NewToolResultError(fmt.Sprintf("agent %q not found", name)), nil
}

// =============================================================================
// Plugins
// =============================================================================

func (h *handlers) listPlugins(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := h.deps.Config()
	if cfg == nil {
		return mcp.NewToolResultError("config not loaded"), nil
	}

	type pluginSummary struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		Source    string `json:"source,omitempty"`
		ToolCount int    `json:"toolCount"`
	}

	plugins := make([]pluginSummary, 0, len(cfg.Plugins))
	for _, p := range cfg.Plugins {
		toolCount := 0
		if client, ok := cfg.LoadedPlugins[p.Name]; ok {
			if tools, err := client.ListTools(); err == nil {
				toolCount = len(tools)
			}
		}
		plugins = append(plugins, pluginSummary{
			Name:      p.Name,
			Version:   p.Version,
			Source:    p.Source,
			ToolCount: toolCount,
		})
	}

	return toolResult(map[string]any{
		"plugins": plugins,
	})
}

func (h *handlers) listPluginTools(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pluginName, _ := req.GetArguments()["plugin_name"].(string)
	if pluginName == "" {
		return mcp.NewToolResultError("plugin_name is required"), nil
	}

	cfg := h.deps.Config()
	if cfg == nil {
		return mcp.NewToolResultError("config not loaded"), nil
	}

	client, ok := cfg.LoadedPlugins[pluginName]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("plugin %q not found or not loaded", pluginName)), nil
	}

	tools, err := client.ListTools()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list tools for plugin %q: %v", pluginName, err)), nil
	}

	type toolSummary struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	toolList := make([]toolSummary, 0, len(tools))
	for _, t := range tools {
		toolList = append(toolList, toolSummary{
			Name:        t.Name,
			Description: t.Description,
		})
	}

	return toolResult(map[string]any{
		"plugin": pluginName,
		"tools":  toolList,
	})
}

func (h *handlers) getPluginTool(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	pluginName, _ := args["plugin_name"].(string)
	toolName, _ := args["tool_name"].(string)

	if pluginName == "" || toolName == "" {
		return mcp.NewToolResultError("plugin_name and tool_name are required"), nil
	}

	cfg := h.deps.Config()
	if cfg == nil {
		return mcp.NewToolResultError("config not loaded"), nil
	}

	client, ok := cfg.LoadedPlugins[pluginName]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("plugin %q not found or not loaded", pluginName)), nil
	}

	info, err := client.GetToolInfo(toolName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("tool %q not found in plugin %q: %v", toolName, pluginName, err)), nil
	}

	return toolResult(map[string]any{
		"plugin": pluginName,
		"tool": map[string]any{
			"name":        info.Name,
			"description": info.Description,
			"schema":      info.Schema,
		},
	})
}

// =============================================================================
// Config Operations
// =============================================================================

func (h *handlers) verifyConfig(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_, err := config.LoadAndValidate(h.deps.ConfigPath)
	if err != nil {
		return toolResult(map[string]any{
			"valid": false,
			"error": err.Error(),
		})
	}
	return toolResult(map[string]any{
		"valid": true,
	})
}

func (h *handlers) reloadConfig(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.deps.ReloadConfig == nil {
		return mcp.NewToolResultError("config reload not available"), nil
	}
	if err := h.deps.ReloadConfig(); err != nil {
		return toolResult(map[string]any{
			"success": false,
			"error":   err.Error(),
		})
	}
	return toolResult(map[string]any{
		"success": true,
	})
}

// =============================================================================
// Variables
// =============================================================================

func maskSecret(value string) string {
	if len(value) <= 4 {
		return strings.Repeat("•", len(value))
	}
	return value[:4] + "••••"
}

func (h *handlers) listVars(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := h.deps.Config()

	fileVars, err := config.LoadVarsFromFile()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to load variables: %v", err)), nil
	}

	var cfgVars []config.Variable
	if cfg != nil {
		cfgVars = cfg.Variables
	}

	type varDetail struct {
		Name     string `json:"name"`
		Secret   bool   `json:"secret"`
		HasValue bool   `json:"hasValue"`
		Source   string `json:"source"`
		Value    string `json:"value,omitempty"`
		Default  string `json:"default,omitempty"`
	}

	details := make([]varDetail, 0, len(cfgVars))
	for _, v := range cfgVars {
		d := varDetail{
			Name:   v.Name,
			Secret: v.Secret,
		}

		if fileVal, ok := fileVars[v.Name]; ok {
			d.HasValue = true
			d.Source = "override"
			if v.Secret {
				d.Value = maskSecret(fileVal)
			} else {
				d.Value = fileVal
			}
		} else if v.Default != "" {
			d.HasValue = true
			d.Source = "default"
			d.Default = v.Default
			if v.Secret {
				d.Value = maskSecret(v.Default)
			} else {
				d.Value = v.Default
			}
		} else {
			d.Source = "unset"
		}

		details = append(details, d)
	}

	return toolResult(map[string]any{
		"variables": details,
	})
}

func (h *handlers) getVar(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, _ := req.GetArguments()["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	cfg := h.deps.Config()

	fileVars, err := config.LoadVarsFromFile()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to load variables: %v", err)), nil
	}

	// Find variable definition in config
	var varDef *config.Variable
	if cfg != nil {
		for _, v := range cfg.Variables {
			if v.Name == name {
				varDef = &v
				break
			}
		}
	}

	if varDef == nil {
		return mcp.NewToolResultError(fmt.Sprintf("variable %q not found in config", name)), nil
	}

	result := map[string]any{
		"name":   varDef.Name,
		"secret": varDef.Secret,
	}

	if fileVal, ok := fileVars[varDef.Name]; ok {
		result["hasValue"] = true
		result["source"] = "override"
		if varDef.Secret {
			result["value"] = maskSecret(fileVal)
		} else {
			result["value"] = fileVal
		}
	} else if varDef.Default != "" {
		result["hasValue"] = true
		result["source"] = "default"
		result["default"] = varDef.Default
		if varDef.Secret {
			result["value"] = maskSecret(varDef.Default)
		} else {
			result["value"] = varDef.Default
		}
	} else {
		result["hasValue"] = false
		result["source"] = "unset"
	}

	return toolResult(result)
}
