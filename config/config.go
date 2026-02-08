package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"

	"squadron/plugin"
)

// Config holds all configuration
type Config struct {
	Models      []Model      `hcl:"model,block"`
	Agents      []Agent      `hcl:"agent,block"`
	Variables   []Variable   `hcl:"variable,block"`
	CustomTools []CustomTool `hcl:"tool,block"`
	Plugins     []Plugin     `hcl:"plugin,block"`
	Workflows   []Workflow   `hcl:"workflow,block"`

	// LoadedPlugins holds the loaded plugin clients, keyed by plugin name
	LoadedPlugins map[string]*plugin.PluginClient `hcl:"-"`
	// PluginWarnings holds warnings for plugins that could not be loaded
	PluginWarnings []string `hcl:"-"`
	// ResolvedVars holds the resolved variable values for runtime use
	ResolvedVars map[string]cty.Value `hcl:"-"`
}

func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return LoadDir(path)
	}
	return LoadFile(path)
}

// LoadAndValidate loads the config and validates all components
func LoadAndValidate(path string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that all config components are valid
func (c *Config) Validate() error {
	for _, m := range c.Models {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("model '%s': %w", m.Name, err)
		}
	}

	for _, v := range c.Variables {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("variable '%s': %w", v.Name, err)
		}
	}

	for _, a := range c.Agents {
		if err := a.Validate(); err != nil {
			return err
		}
	}

	// Validate plugins
	for _, p := range c.Plugins {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("plugin '%s': %w", p.Name, err)
		}
	}

	// Validate custom tools and check for reserved name conflicts
	for _, t := range c.CustomTools {
		if err := t.Validate(); err != nil {
			return err
		}
		// Check that custom tool names don't conflict with internal tools
		if IsInternalTool(t.Name) {
			return fmt.Errorf("custom tool '%s': name conflicts with internal tool", t.Name)
		}
	}

	// Build valid tool references for validation
	// Format: plugins.{namespace}.{tool} for internal/external plugins
	//         tools.{name} for custom tools
	validToolRefs := make(map[string]bool)

	// Add internal plugin tools (plugins.bash.bash, plugins.http.get, etc.)
	for namespace, tools := range InternalPluginTools {
		for _, toolName := range tools {
			validToolRefs[fmt.Sprintf("plugins.%s.%s", namespace, toolName)] = true
		}
		// Add "all" marker for loading all tools from this plugin
		validToolRefs[fmt.Sprintf("plugins.%s.all", namespace)] = true
	}

	// Add external plugin tools
	for pluginName, client := range c.LoadedPlugins {
		tools, err := client.ListTools()
		if err == nil {
			for _, t := range tools {
				validToolRefs[fmt.Sprintf("plugins.%s.%s", pluginName, t.Name)] = true
			}
		}
		// Add "all" marker for loading all tools from this plugin
		validToolRefs[fmt.Sprintf("plugins.%s.all", pluginName)] = true
	}

	// Add custom tools (both tools.{name} and bare {name} for backwards compatibility)
	for _, t := range c.CustomTools {
		validToolRefs[fmt.Sprintf("tools.%s", t.Name)] = true
		validToolRefs[t.Name] = true // legacy bare name
	}

	// Validate tool references in agents
	for _, a := range c.Agents {
		for _, toolRef := range a.Tools {
			if !validToolRefs[toolRef] {
				return fmt.Errorf("agent '%s': unknown tool '%s'. Available tools: %v", a.Name, toolRef, getToolNames(validToolRefs))
			}
		}
	}

	// Validate workflows
	for _, w := range c.Workflows {
		if err := w.Validate(c.Models, c.Agents); err != nil {
			return fmt.Errorf("workflow '%s': %w", w.Name, err)
		}
	}

	return nil
}

// getToolNames returns a sorted list of tool names from the map
func getToolNames(tools map[string]bool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	return names
}

func LoadFile(filename string) (*Config, error) {
	return loadFromFiles([]string{filename})
}

func LoadDir(dir string) (*Config, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.hcl"))
	if err != nil {
		return nil, err
	}
	return loadFromFiles(files)
}

// parsedBlocks holds all blocks extracted from a file in one pass
type parsedBlocks struct {
	Variables []*hcl.Block
	Models    []*hcl.Block
	Agents    []*hcl.Block
	Tools     []*hcl.Block
	Plugins   []*hcl.Block
	Workflows []*hcl.Block
}

// loadFromFiles implements staged loading: variables → models → agents → tools
func loadFromFiles(files []string) (*Config, error) {
	// Parse all files and extract all block types in a single pass
	parser := hclparse.NewParser()
	var allParsedBlocks []parsedBlocks

	for _, file := range files {
		hclFile, diags := parser.ParseHCLFile(file)
		if diags.HasErrors() {
			return nil, fmt.Errorf("[1] parse %s: %w", file, diags)
		}

		// Extract all known block types in one PartialContent call
		content, _, diags := hclFile.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{
				{Type: "variable", LabelNames: []string{"name"}},
				{Type: "model", LabelNames: []string{"name"}},
				{Type: "agent", LabelNames: []string{"name"}},
				{Type: "tool", LabelNames: []string{"name"}},
				{Type: "plugin", LabelNames: []string{"name"}},
				{Type: "workflow", LabelNames: []string{"name"}},
			},
		})
		if diags.HasErrors() {
			return nil, fmt.Errorf("[2] partial content %s: %w", file, diags)
		}

		var pb parsedBlocks
		for _, block := range content.Blocks {
			switch block.Type {
			case "variable":
				pb.Variables = append(pb.Variables, block)
			case "model":
				pb.Models = append(pb.Models, block)
			case "agent":
				pb.Agents = append(pb.Agents, block)
			case "tool":
				pb.Tools = append(pb.Tools, block)
			case "plugin":
				pb.Plugins = append(pb.Plugins, block)
			case "workflow":
				pb.Workflows = append(pb.Workflows, block)
			}
		}
		allParsedBlocks = append(allParsedBlocks, pb)
	}

	// Stage 1: Load variables (no context needed)
	var allVars []Variable
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Variables {
			var v Variable
			v.Name = block.Labels[0]
			diags := gohcl.DecodeBody(block.Body, nil, &v)
			if diags.HasErrors() {
				return nil, fmt.Errorf("[3] decode variable %s: %w", v.Name, diags)
			}
			allVars = append(allVars, v)
		}
	}

	// Build vars context
	varsCtx, resolvedVars := buildVarsContext(allVars)

	// Stage 1.5: Load plugins (with vars context - plugins are simple, load early so tools can reference them)
	var allPlugins []Plugin
	var pluginWarnings []string
	loadedPlugins := make(map[string]*plugin.PluginClient)

	for _, pb := range allParsedBlocks {
		for _, block := range pb.Plugins {
			p, err := parsePluginBlock(block, varsCtx)
			if err != nil {
				return nil, err
			}
			allPlugins = append(allPlugins, *p)

			// Try to load the plugin (passes source for auto-download if not found locally)
			client, err := plugin.LoadPlugin(p.Name, p.Version, p.Source)
			if err != nil {
				pluginWarnings = append(pluginWarnings, fmt.Sprintf("plugin '%s' (version %s): %v", p.Name, p.Version, err))
			} else {
				// Configure the plugin with settings if any
				if len(p.Settings) > 0 {
					if err := client.Configure(p.Settings); err != nil {
						pluginWarnings = append(pluginWarnings, fmt.Sprintf("plugin '%s' configure: %v", p.Name, err))
						client.Close()
						continue
					}
				}
				loadedPlugins[p.Name] = client
			}
		}
	}

	// Build plugins context for HCL evaluation
	pluginsCtx := buildPluginsContext(varsCtx, loadedPlugins)

	// Stage 2: Load models (with vars + plugins context)
	var allModels []Model
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Models {
			var m Model
			m.Name = block.Labels[0]
			diags := gohcl.DecodeBody(block.Body, pluginsCtx, &m)
			if diags.HasErrors() {
				return nil, diags
			}
			allModels = append(allModels, m)
		}
	}

	// Build models context (add to plugins context)
	modelsCtx := buildModelsContext(pluginsCtx, allModels)

	// Stage 3: Load custom tools (with vars + models + plugins context, plus dynamic field parsing)
	var allTools []CustomTool
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Tools {
			tool, err := parseToolBlock(block, modelsCtx, loadedPlugins)
			if err != nil {
				return nil, err
			}
			allTools = append(allTools, *tool)
		}
	}

	// Build tools context (add to models context) - includes both internal and custom tools
	fullCtx := buildToolsContext(modelsCtx, allTools)

	// Stage 4: Load agents (with vars + models + tools context)
	var allAgents []Agent
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Agents {
			var a Agent
			a.Name = block.Labels[0]
			diags := gohcl.DecodeBody(block.Body, fullCtx, &a)
			if diags.HasErrors() {
				return nil, diags
			}
			allAgents = append(allAgents, a)
		}
	}

	// Build agents context (add to full context)
	agentsCtx := buildAgentsContext(fullCtx, allAgents)

	// Stage 5: Load workflows (with vars + models + tools + agents context)
	var allWorkflows []Workflow
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Workflows {
			workflow, err := parseWorkflowBlock(block, agentsCtx)
			if err != nil {
				return nil, err
			}
			allWorkflows = append(allWorkflows, *workflow)
		}
	}

	return &Config{
		Variables:      allVars,
		Models:         allModels,
		Agents:         allAgents,
		CustomTools:    allTools,
		Plugins:        allPlugins,
		Workflows:      allWorkflows,
		LoadedPlugins:  loadedPlugins,
		PluginWarnings: pluginWarnings,
		ResolvedVars:   resolvedVars,
	}, nil
}

// inputFieldBlock is used for parsing input field blocks
type inputFieldBlock struct {
	Name        string `hcl:"name,label"`
	Type        string `hcl:"type"`
	Description string `hcl:"description,optional"`
	Required    bool   `hcl:"required,optional"`
}

// inputsBlock is used for parsing the inputs block
type inputsBlock struct {
	Fields []inputFieldBlock `hcl:"field,block"`
}

// parseToolBlock parses a single tool block with dynamic fields based on implemented tool schema
func parseToolBlock(block *hcl.Block, baseCtx *hcl.EvalContext, loadedPlugins map[string]*plugin.PluginClient) (*CustomTool, error) {
	toolName := block.Labels[0]

	// Parse the tool block content: static fields (implements, description) + inputs block + dynamic fields
	toolContent, remainBody, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "implements", Required: true},
			{Name: "description"},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "inputs"},
		},
	})
	if diags.HasErrors() {
		return nil, diags
	}

	// Get implements attribute
	implementsAttr := toolContent.Attributes["implements"]
	implementsVal, diags := implementsAttr.Expr.Value(baseCtx)
	if diags.HasErrors() {
		return nil, diags
	}
	implements := implementsVal.AsString()

	// Get optional description
	var description string
	if descAttr, ok := toolContent.Attributes["description"]; ok {
		descVal, diags := descAttr.Expr.Value(baseCtx)
		if diags.HasErrors() {
			return nil, diags
		}
		description = descVal.AsString()
	}

	tool := &CustomTool{
		Name:        toolName,
		Implements:  implements,
		Description: description,
		Inputs:      nil,
		FieldExprs:  make(map[string]hcl.Expression),
	}

	// Get the implemented tool's schema (supports both internal and plugin tools)
	implTool := tool.GetImplementedToolWithPlugins(loadedPlugins)
	if implTool == nil {
		return nil, fmt.Errorf("tool '%s': unknown implemented tool '%s'", toolName, implements)
	}

	// Parse inputs block if present
	for _, blk := range toolContent.Blocks {
		if blk.Type == "inputs" {
			var parsedInputs inputsBlock
			diags := gohcl.DecodeBody(blk.Body, nil, &parsedInputs)
			if diags.HasErrors() {
				return nil, diags
			}

			tool.Inputs = &InputsSchema{}
			for _, f := range parsedInputs.Fields {
				tool.Inputs.Fields = append(tool.Inputs.Fields, InputField{
					Name:        f.Name,
					Type:        f.Type,
					Description: f.Description,
					Required:    f.Required,
				})
			}
		}
	}

	// Build eval context with inputs placeholder to validate expressions
	inputsType := tool.BuildInputsCtyType()
	toolCtx := BuildFieldsEvalContext(baseCtx, inputsType)

	// Get the remaining attributes (dynamic fields from the implemented tool's schema)
	// Build schema for remaining attributes based on implemented tool's schema
	implSchema := implTool.ToolPayloadSchema()
	var attrSchemas []hcl.AttributeSchema
	for propName := range implSchema.Properties {
		attrSchemas = append(attrSchemas, hcl.AttributeSchema{Name: propName})
	}

	remainContent, _, diags := remainBody.PartialContent(&hcl.BodySchema{
		Attributes: attrSchemas,
	})
	if diags.HasErrors() {
		return nil, diags
	}

	for attrName, attr := range remainContent.Attributes {
		// Verify this is a valid field from the implemented tool's schema
		if _, ok := implSchema.Properties[attrName]; !ok {
			return nil, fmt.Errorf("tool '%s': unknown field '%s' - not part of '%s' tool schema", toolName, attrName, implements)
		}

		// Validate the expression can be evaluated (with unknown inputs)
		_, diags := attr.Expr.Value(toolCtx)
		if diags.HasErrors() {
			return nil, diags
		}

		// Store the expression for runtime evaluation
		tool.FieldExprs[attrName] = attr.Expr
	}

	return tool, nil
}

// buildVarsContext creates context with just vars
func buildVarsContext(vars []Variable) (*hcl.EvalContext, map[string]cty.Value) {
	varsMap := make(map[string]cty.Value)
	fileVars, _ := LoadVarsFromFile()
	for _, v := range vars {
		if val, ok := fileVars[v.Name]; ok {
			varsMap[v.Name] = cty.StringVal(val)
		} else if v.Default != "" {
			varsMap[v.Name] = cty.StringVal(v.Default)
		} else {
			varsMap[v.Name] = cty.StringVal("")
		}
	}

	return &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"vars": cty.ObjectVal(varsMap),
		},
	}, varsMap
}

// buildModelsContext adds models to existing context
func buildModelsContext(ctx *hcl.EvalContext, models []Model) *hcl.EvalContext {
	modelsMap := make(map[string]cty.Value)
	for _, m := range models {
		providerModels := make(map[string]cty.Value)
		for _, modelKey := range m.AllowedModels {
			providerModels[modelKey] = cty.StringVal(modelKey)
		}
		modelsMap[m.Name] = cty.ObjectVal(providerModels)
	}

	// Copy existing vars and add models
	newVars := make(map[string]cty.Value)
	for k, v := range ctx.Variables {
		newVars[k] = v
	}
	newVars["models"] = cty.ObjectVal(modelsMap)

	return &hcl.EvalContext{
		Variables: newVars,
	}
}

// buildToolsContext adds tools namespace to existing context (custom tools only)
// Internal tools are now in the plugins namespace (plugins.bash.bash, plugins.http.get)
func buildToolsContext(ctx *hcl.EvalContext, customTools []CustomTool) *hcl.EvalContext {
	toolsMap := make(map[string]cty.Value)

	// Add custom tools with tools.{name} reference format
	for _, t := range customTools {
		toolsMap[t.Name] = cty.StringVal(fmt.Sprintf("tools.%s", t.Name))
	}

	// Copy existing vars and add tools
	newVars := make(map[string]cty.Value)
	for k, v := range ctx.Variables {
		newVars[k] = v
	}
	newVars["tools"] = cty.ObjectVal(toolsMap)

	return &hcl.EvalContext{
		Variables: newVars,
	}
}

// buildPluginsContext adds plugins namespace to existing context
// Creates plugins.{plugin_name}.{tool_name} references
// Includes both internal tools (bash, http) and external plugins
func buildPluginsContext(ctx *hcl.EvalContext, loadedPlugins map[string]*plugin.PluginClient) *hcl.EvalContext {
	pluginsMap := make(map[string]cty.Value)

	// Add internal plugin namespaces (bash, http)
	for namespace, tools := range InternalPluginTools {
		toolsMap := make(map[string]cty.Value)
		for _, toolName := range tools {
			toolsMap[toolName] = cty.StringVal(fmt.Sprintf("plugins.%s.%s", namespace, toolName))
		}
		// Add "all" marker that expands to all tools from this plugin
		toolsMap["all"] = cty.StringVal(fmt.Sprintf("plugins.%s.all", namespace))
		pluginsMap[namespace] = cty.ObjectVal(toolsMap)
	}

	// Add external plugins
	for pluginName, client := range loadedPlugins {
		toolsMap := make(map[string]cty.Value)
		tools, err := client.ListTools()
		if err == nil {
			for _, t := range tools {
				// Value is "plugins.{plugin_name}.{tool_name}" to identify the source
				toolsMap[t.Name] = cty.StringVal(fmt.Sprintf("plugins.%s.%s", pluginName, t.Name))
			}
		}
		// Add "all" marker that expands to all tools from this plugin
		toolsMap["all"] = cty.StringVal(fmt.Sprintf("plugins.%s.all", pluginName))
		pluginsMap[pluginName] = cty.ObjectVal(toolsMap)
	}

	// Copy existing vars and add plugins
	newVars := make(map[string]cty.Value)
	for k, v := range ctx.Variables {
		newVars[k] = v
	}
	newVars["plugins"] = cty.ObjectVal(pluginsMap)

	return &hcl.EvalContext{
		Variables: newVars,
	}
}

// GetPluginTool returns a plugin tool by its implements string (e.g., "plugins.pinger.echo")
func (c *Config) GetPluginTool(implements string) (*plugin.PluginClient, string, error) {
	parts := strings.Split(implements, ".")
	if len(parts) != 3 || parts[0] != "plugins" {
		return nil, "", fmt.Errorf("invalid plugin tool reference: %s", implements)
	}

	pluginName := parts[1]
	toolName := parts[2]

	client, ok := c.LoadedPlugins[pluginName]
	if !ok {
		return nil, "", fmt.Errorf("plugin '%s' not loaded", pluginName)
	}

	return client, toolName, nil
}

// buildAgentsContext adds agents namespace to existing context
// Creates agents.{agent_name} references
func buildAgentsContext(ctx *hcl.EvalContext, agents []Agent) *hcl.EvalContext {
	agentsMap := make(map[string]cty.Value)
	for _, a := range agents {
		agentsMap[a.Name] = cty.StringVal(a.Name)
	}

	// Copy existing vars and add agents
	newVars := make(map[string]cty.Value)
	for k, v := range ctx.Variables {
		newVars[k] = v
	}
	newVars["agents"] = cty.ObjectVal(agentsMap)

	return &hcl.EvalContext{
		Variables: newVars,
	}
}

// workflowTaskBlock is used for parsing task blocks within a workflow
type workflowTaskBlock struct {
	Name      string   `hcl:"name,label"`
	Objective string   `hcl:"objective"`
	Agents    []string `hcl:"agents"`
	DependsOn []string `hcl:"depends_on,optional"`
}

// parseWorkflowBlock parses a workflow block with its nested task blocks
func parseWorkflowBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Workflow, error) {
	workflowName := block.Labels[0]

	// Parse the workflow block content
	workflowContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "supervisor_model", Required: true},
			{Name: "agents", Required: true},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "task", LabelNames: []string{"name"}},
			{Type: "input", LabelNames: []string{"name"}},
			{Type: "dataset", LabelNames: []string{"name"}},
			{Type: "secret", LabelNames: []string{"name"}},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("workflow '%s': %w", workflowName, diags)
	}

	// Get supervisor_model attribute
	supervisorModelAttr := workflowContent.Attributes["supervisor_model"]
	supervisorModelVal, diags := supervisorModelAttr.Expr.Value(ctx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("workflow '%s': %w", workflowName, diags)
	}

	// Get agents attribute (workflow-level agents)
	agentsAttr := workflowContent.Attributes["agents"]
	agentsVal, diags := agentsAttr.Expr.Value(ctx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("workflow '%s': %w", workflowName, diags)
	}

	var workflowAgents []string
	for it := agentsVal.ElementIterator(); it.Next(); {
		_, v := it.Element()
		workflowAgents = append(workflowAgents, v.AsString())
	}

	workflow := &Workflow{
		Name:            workflowName,
		SupervisorModel: supervisorModelVal.AsString(),
		Agents:          workflowAgents,
	}

	// Parse input blocks first
	for _, inputBlock := range workflowContent.Blocks {
		if inputBlock.Type != "input" {
			continue
		}
		input, err := parseWorkflowInputBlock(inputBlock, ctx)
		if err != nil {
			return nil, fmt.Errorf("workflow '%s': %w", workflowName, err)
		}
		workflow.Inputs = append(workflow.Inputs, *input)
	}

	// Build inputs context with placeholder values for dataset bind_to validation
	inputsType := workflow.BuildInputsCtyType()
	inputsCtx := &hcl.EvalContext{
		Variables: make(map[string]cty.Value),
	}
	for k, v := range ctx.Variables {
		inputsCtx.Variables[k] = v
	}
	inputsCtx.Variables["inputs"] = cty.UnknownVal(inputsType)

	// Parse dataset blocks
	for _, datasetBlock := range workflowContent.Blocks {
		if datasetBlock.Type != "dataset" {
			continue
		}
		dataset, err := parseDatasetBlock(datasetBlock, inputsCtx)
		if err != nil {
			return nil, fmt.Errorf("workflow '%s': %w", workflowName, err)
		}
		workflow.Datasets = append(workflow.Datasets, *dataset)
	}

	// Build tasks context for depends_on references
	taskNames := make(map[string]cty.Value)
	for _, taskBlock := range workflowContent.Blocks {
		if taskBlock.Type == "task" {
			taskNames[taskBlock.Labels[0]] = cty.StringVal(taskBlock.Labels[0])
		}
	}

	// Build datasets context for iterator references
	datasetNames := make(map[string]cty.Value)
	for _, ds := range workflow.Datasets {
		datasetNames[ds.Name] = cty.StringVal(ds.Name)
	}

	// Add tasks, inputs, datasets, and item namespaces to context
	taskCtx := &hcl.EvalContext{
		Variables: make(map[string]cty.Value),
	}
	for k, v := range ctx.Variables {
		taskCtx.Variables[k] = v
	}
	taskCtx.Variables["tasks"] = cty.ObjectVal(taskNames)
	taskCtx.Variables["inputs"] = cty.UnknownVal(inputsType) // Placeholder for validation
	taskCtx.Variables["datasets"] = cty.ObjectVal(datasetNames)
	taskCtx.Variables["item"] = cty.DynamicVal // Placeholder for iteration item

	// Parse task blocks
	for _, taskBlock := range workflowContent.Blocks {
		if taskBlock.Type != "task" {
			continue
		}

		task, err := parseTaskBlock(taskBlock, taskCtx)
		if err != nil {
			return nil, fmt.Errorf("workflow '%s': %w", workflowName, err)
		}
		workflow.Tasks = append(workflow.Tasks, *task)
	}

	return workflow, nil
}

// parseWorkflowInputBlock parses an input block within a workflow
func parseWorkflowInputBlock(block *hcl.Block, ctx *hcl.EvalContext) (*WorkflowInput, error) {
	inputName := block.Labels[0]

	inputContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "type", Required: true},
			{Name: "description"},
			{Name: "default"},
			{Name: "secret"},
			{Name: "value"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("input '%s': %w", inputName, diags)
	}

	// Get type
	typeVal, diags := inputContent.Attributes["type"].Expr.Value(ctx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("input '%s': %w", inputName, diags)
	}

	input := &WorkflowInput{
		Name: inputName,
		Type: typeVal.AsString(),
	}

	// Get optional description
	if descAttr, ok := inputContent.Attributes["description"]; ok {
		descVal, diags := descAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("input '%s': %w", inputName, diags)
		}
		input.Description = descVal.AsString()
	}

	// Get optional default
	if defaultAttr, ok := inputContent.Attributes["default"]; ok {
		defaultVal, diags := defaultAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("input '%s': %w", inputName, diags)
		}
		input.Default = &defaultVal
	}

	// Get optional secret flag
	if secretAttr, ok := inputContent.Attributes["secret"]; ok {
		secretVal, diags := secretAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("input '%s': %w", inputName, diags)
		}
		input.Secret = secretVal.True()
	}

	// Get optional value (required for secrets, from vars.* or literal)
	if valueAttr, ok := inputContent.Attributes["value"]; ok {
		valueVal, diags := valueAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("input '%s': %w", inputName, diags)
		}
		input.Value = &valueVal
	}

	return input, nil
}

// parseDatasetBlock parses a dataset block within a workflow
func parseDatasetBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Dataset, error) {
	datasetName := block.Labels[0]

	datasetContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "description"},
			{Name: "bind_to"},
			{Name: "items"},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "schema"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("dataset '%s': %w", datasetName, diags)
	}

	dataset := &Dataset{
		Name: datasetName,
	}

	// Get optional description
	if descAttr, ok := datasetContent.Attributes["description"]; ok {
		descVal, diags := descAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("dataset '%s': %w", datasetName, diags)
		}
		dataset.Description = descVal.AsString()
	}

	// Get optional bind_to - store the expression for deferred evaluation
	// and extract the input name for validation
	if bindAttr, ok := datasetContent.Attributes["bind_to"]; ok {
		dataset.BindToExpr = bindAttr.Expr

		// Extract the input name from the traversal
		// The expression should be "inputs.{input_name}"
		traversal, travDiags := hcl.AbsTraversalForExpr(bindAttr.Expr)
		if travDiags.HasErrors() || len(traversal) < 2 {
			return nil, fmt.Errorf("dataset '%s': bind_to must be in format inputs.{input_name}", datasetName)
		}
		if traversal.RootName() != "inputs" {
			return nil, fmt.Errorf("dataset '%s': bind_to must reference inputs.{input_name}", datasetName)
		}
		if attr, ok := traversal[1].(hcl.TraverseAttr); ok {
			dataset.BindTo = attr.Name
		} else {
			return nil, fmt.Errorf("dataset '%s': bind_to must be in format inputs.{input_name}", datasetName)
		}
	}

	// Get optional items (inline list of items)
	if itemsAttr, ok := datasetContent.Attributes["items"]; ok {
		itemsVal, diags := itemsAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("dataset '%s': %w", datasetName, diags)
		}
		// Convert to list of cty.Value
		if itemsVal.CanIterateElements() {
			for it := itemsVal.ElementIterator(); it.Next(); {
				_, v := it.Element()
				dataset.Items = append(dataset.Items, v)
			}
		} else {
			return nil, fmt.Errorf("dataset '%s': items must be a list", datasetName)
		}
	}

	// Parse schema block if present
	for _, schemaBlock := range datasetContent.Blocks {
		if schemaBlock.Type == "schema" {
			schema, err := parseSchemaBlock(schemaBlock)
			if err != nil {
				return nil, fmt.Errorf("dataset '%s': %w", datasetName, err)
			}
			dataset.Schema = schema
		}
	}

	return dataset, nil
}

// parseSchemaBlock parses a schema block (reuses inputFieldBlock pattern)
func parseSchemaBlock(block *hcl.Block) (*InputsSchema, error) {
	var schemaContent struct {
		Fields []inputFieldBlock `hcl:"field,block"`
	}
	diags := gohcl.DecodeBody(block.Body, nil, &schemaContent)
	if diags.HasErrors() {
		return nil, diags
	}

	schema := &InputsSchema{}
	for _, f := range schemaContent.Fields {
		schema.Fields = append(schema.Fields, InputField{
			Name:        f.Name,
			Type:        f.Type,
			Description: f.Description,
			Required:    f.Required,
		})
	}
	return schema, nil
}

// parseIteratorBlock parses an iterator block within a task
func parseIteratorBlock(block *hcl.Block, ctx *hcl.EvalContext) (*TaskIterator, error) {
	iterContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "dataset", Required: true},
			{Name: "parallel"},
			{Name: "max_retries"},
			{Name: "concurrency_limit"},
			{Name: "start_delay"},
			{Name: "smoketest"},
		},
	})
	if diags.HasErrors() {
		return nil, diags
	}

	// Get dataset reference
	datasetVal, diags := iterContent.Attributes["dataset"].Expr.Value(ctx)
	if diags.HasErrors() {
		return nil, diags
	}

	iterator := &TaskIterator{
		Dataset:          datasetVal.AsString(),
		Parallel:         false, // Default to sequential
		MaxRetries:       0,     // Default to no retries
		ConcurrencyLimit: 5,     // Default to 5 concurrent iterations
	}

	// Get optional parallel flag
	if parallelAttr, ok := iterContent.Attributes["parallel"]; ok {
		parallelVal, diags := parallelAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, diags
		}
		iterator.Parallel = parallelVal.True()
	}

	// Get optional max_retries
	if maxRetriesAttr, ok := iterContent.Attributes["max_retries"]; ok {
		maxRetriesVal, diags := maxRetriesAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, diags
		}
		// Convert cty.Number to int
		bf := maxRetriesVal.AsBigFloat()
		intVal, _ := bf.Int64()
		iterator.MaxRetries = int(intVal)
	}

	// Get optional concurrency_limit (only applies when parallel=true)
	if concurrencyAttr, ok := iterContent.Attributes["concurrency_limit"]; ok {
		concurrencyVal, diags := concurrencyAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, diags
		}
		// Convert cty.Number to int
		bf := concurrencyVal.AsBigFloat()
		intVal, _ := bf.Int64()
		if intVal > 0 {
			iterator.ConcurrencyLimit = int(intVal)
		}
	}

	// Get optional start_delay (milliseconds delay between starts in first batch)
	if startDelayAttr, ok := iterContent.Attributes["start_delay"]; ok {
		startDelayVal, diags := startDelayAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, diags
		}
		bf := startDelayVal.AsBigFloat()
		intVal, _ := bf.Int64()
		if intVal > 0 {
			iterator.StartDelay = int(intVal)
		}
	}

	// Get optional smoketest (run first iteration completely before starting others)
	if smoketestAttr, ok := iterContent.Attributes["smoketest"]; ok {
		smoketestVal, diags := smoketestAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, diags
		}
		iterator.Smoketest = smoketestVal.True()
	}

	// Validate: parallel-specific options are only valid when parallel=true
	if !iterator.Parallel {
		if _, ok := iterContent.Attributes["concurrency_limit"]; ok {
			return nil, fmt.Errorf("concurrency_limit is only valid when parallel=true")
		}
		if _, ok := iterContent.Attributes["start_delay"]; ok {
			return nil, fmt.Errorf("start_delay is only valid when parallel=true")
		}
		if _, ok := iterContent.Attributes["smoketest"]; ok {
			return nil, fmt.Errorf("smoketest is only valid when parallel=true")
		}
	}

	return iterator, nil
}

// parseTaskBlock parses a single task block within a workflow
func parseTaskBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Task, error) {
	taskName := block.Labels[0]

	// Parse task attributes and blocks
	taskContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "objective", Required: true},
			{Name: "agents"},    // Optional - uses workflow-level agents if not specified
			{Name: "depends_on"},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "iterator"},
			{Type: "output"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("task '%s': %w", taskName, diags)
	}

	// Store the objective expression for deferred evaluation
	// Validate that it can be parsed (with unknown inputs for placeholders)
	objectiveExpr := taskContent.Attributes["objective"].Expr
	_, diags = objectiveExpr.Value(ctx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("task '%s': %w", taskName, diags)
	}

	// Get agents (optional array of agent references)
	var agents []string
	if agentsAttr, ok := taskContent.Attributes["agents"]; ok {
		agentsVal, diags := agentsAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("task '%s': %w", taskName, diags)
		}
		for it := agentsVal.ElementIterator(); it.Next(); {
			_, v := it.Element()
			agents = append(agents, v.AsString())
		}
	}

	// Get depends_on (optional array of task references)
	var dependsOn []string
	if depAttr, ok := taskContent.Attributes["depends_on"]; ok {
		depVal, diags := depAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("task '%s': %w", taskName, diags)
		}
		for it := depVal.ElementIterator(); it.Next(); {
			_, v := it.Element()
			dependsOn = append(dependsOn, v.AsString())
		}
	}

	// Parse iterator block if present
	var iterator *TaskIterator
	for _, iterBlock := range taskContent.Blocks {
		if iterBlock.Type == "iterator" {
			iter, err := parseIteratorBlock(iterBlock, ctx)
			if err != nil {
				return nil, fmt.Errorf("task '%s': %w", taskName, err)
			}
			iterator = iter
			break
		}
	}

	// Parse output block if present
	var output *OutputSchema
	for _, outputBlock := range taskContent.Blocks {
		if outputBlock.Type == "output" {
			out, err := parseOutputBlock(outputBlock)
			if err != nil {
				return nil, fmt.Errorf("task '%s': %w", taskName, err)
			}
			output = out
			break
		}
	}

	return &Task{
		Name:          taskName,
		ObjectiveExpr: objectiveExpr,
		Agents:        agents,
		DependsOn:     dependsOn,
		Iterator:      iterator,
		Output:        output,
	}, nil
}

// outputFieldBlock is used for parsing output field blocks
type outputFieldBlock struct {
	Name        string `hcl:"name,label"`
	Type        string `hcl:"type"`
	Description string `hcl:"description,optional"`
	Required    bool   `hcl:"required,optional"`
}

// parseOutputBlock parses an output block within a task
func parseOutputBlock(block *hcl.Block) (*OutputSchema, error) {
	var outputContent struct {
		Fields []outputFieldBlock `hcl:"field,block"`
	}
	diags := gohcl.DecodeBody(block.Body, nil, &outputContent)
	if diags.HasErrors() {
		return nil, diags
	}

	output := &OutputSchema{}
	for _, f := range outputContent.Fields {
		output.Fields = append(output.Fields, OutputField{
			Name:        f.Name,
			Type:        f.Type,
			Description: f.Description,
			Required:    f.Required,
		})
	}
	return output, nil
}

// parsePluginBlock parses a plugin block with optional settings
func parsePluginBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Plugin, error) {
	pluginName := block.Labels[0]

	// Parse the plugin block content
	pluginContent, remainBody, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "source", Required: true},
			{Name: "version", Required: true},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "settings"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("plugin '%s': %w", pluginName, diags)
	}

	// Get source
	sourceVal, diags := pluginContent.Attributes["source"].Expr.Value(ctx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("plugin '%s': %w", pluginName, diags)
	}

	// Get version
	versionVal, diags := pluginContent.Attributes["version"].Expr.Value(ctx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("plugin '%s': %w", pluginName, diags)
	}

	p := &Plugin{
		Name:     pluginName,
		Source:   sourceVal.AsString(),
		Version:  versionVal.AsString(),
		Settings: make(map[string]string),
	}

	// Parse settings block if present
	for _, settingsBlock := range pluginContent.Blocks {
		if settingsBlock.Type == "settings" {
			// Get all attributes from settings block dynamically
			settingsContent, _, diags := settingsBlock.Body.PartialContent(&hcl.BodySchema{})
			if diags.HasErrors() {
				return nil, fmt.Errorf("plugin '%s' settings: %w", pluginName, diags)
			}

			// Parse remaining body as JustAttributes to get all settings
			attrs, diags := settingsBlock.Body.JustAttributes()
			if diags.HasErrors() {
				return nil, fmt.Errorf("plugin '%s' settings: %w", pluginName, diags)
			}

			for name, attr := range attrs {
				val, diags := attr.Expr.Value(ctx)
				if diags.HasErrors() {
					return nil, fmt.Errorf("plugin '%s' setting '%s': %w", pluginName, name, diags)
				}
				// Convert to string
				if val.Type() == cty.String {
					p.Settings[name] = val.AsString()
				} else if val.Type() == cty.Bool {
					p.Settings[name] = fmt.Sprintf("%v", val.True())
				} else if val.Type() == cty.Number {
					bf := val.AsBigFloat()
					p.Settings[name] = bf.String()
				} else {
					p.Settings[name] = val.GoString()
				}
			}

			_ = settingsContent // Used for block iteration
		}
	}

	_ = remainBody // No remaining body expected
	return p, nil
}
