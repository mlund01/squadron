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

	schemafunc "squadron/config/functions"
	"squadron/internal/paths"
	"squadron/plugin"

	"github.com/zclconf/go-cty/cty/function"
)

// configFuncs holds the HCL functions map for the current config load.
// Set at the start of loadFromFiles and used by all buildXxxContext helpers.
var configFuncs map[string]function.Function

// Config holds all configuration
type Config struct {
	Models      []Model      `hcl:"model,block"`
	Agents      []Agent      `hcl:"agent,block"`
	Variables   []Variable   `hcl:"variable,block"`
	CustomTools []CustomTool `hcl:"tool,block"`
	Plugins     []Plugin     `hcl:"plugin,block"`
	Missions   []Mission   `hcl:"mission,block"`
	Skills     []Skill     `hcl:"-"`

	// Storage configuration (optional, defaults to memory backend)
	Storage *StorageConfig `hcl:"-"`

	// CommandCenter configuration (optional, nil when absent = standalone mode)
	CommandCenter *CommandCenterConfig `hcl:"-"`

	// MCP server configuration (optional, nil when absent = no MCP server)
	MCP *MCPConfig `hcl:"-"`

	// File browser configurations (optional)
	SharedFolders []SharedFolder `hcl:"-"`

	// LoadedPlugins holds the loaded plugin clients, keyed by plugin name
	LoadedPlugins map[string]*plugin.PluginClient `hcl:"-"`
	// ResolvedVars holds the resolved variable values for runtime use
	ResolvedVars map[string]cty.Value `hcl:"-"`
}

func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config path %q does not exist — create a directory with .hcl config files or pass a valid path with -c", path)
		}
		return nil, err
	}

	if info.IsDir() {
		return LoadDir(path)
	}
	return LoadFile(path)
}

// LoadPartial attempts a best-effort load of the config, returning whatever
// could be parsed (variables, plugin definitions) even if full loading fails.
// The returned Config is never nil. The error indicates whether the full load succeeded.
func LoadPartial(path string) (*Config, error) {
	cfg, err := LoadAndValidate(path)
	if err == nil {
		return cfg, nil
	}

	// Full load failed — try to extract variables and plugin defs from the HCL
	partial := &Config{}
	files, fileErr := resolveConfigFiles(path)
	if fileErr != nil {
		return partial, err // can't even find files, return original error
	}

	parser := hclparse.NewParser()
	for _, file := range files {
		hclFile, diags := parser.ParseHCLFile(file)
		if diags.HasErrors() {
			continue // skip unparseable files
		}
		content, _, diags := hclFile.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{
				{Type: "variable", LabelNames: []string{"name"}},
				{Type: "plugin", LabelNames: []string{"name"}},
				{Type: "command_center"},
				{Type: "mcp"},
			},
		})
		if diags.HasErrors() {
			continue
		}
		for _, block := range content.Blocks {
			switch block.Type {
			case "variable":
				var v Variable
				v.Name = block.Labels[0]
				if diags := gohcl.DecodeBody(block.Body, nil, &v); !diags.HasErrors() {
					partial.Variables = append(partial.Variables, v)
				}
			case "plugin":
				p := Plugin{Name: block.Labels[0]}
				// Use PartialContent to extract source/version while allowing settings block
				pluginContent, _, _ := block.Body.PartialContent(&hcl.BodySchema{
					Attributes: []hcl.AttributeSchema{
						{Name: "source"},
						{Name: "version"},
					},
					Blocks: []hcl.BlockHeaderSchema{
						{Type: "settings"},
					},
				})
				if pluginContent != nil {
					if attr, ok := pluginContent.Attributes["source"]; ok {
						if val, diags := attr.Expr.Value(nil); !diags.HasErrors() {
							p.Source = val.AsString()
						}
					}
					if attr, ok := pluginContent.Attributes["version"]; ok {
						if val, diags := attr.Expr.Value(nil); !diags.HasErrors() {
							p.Version = val.AsString()
						}
					}
				}
				partial.Plugins = append(partial.Plugins, p)
			case "command_center":
				if partial.CommandCenter == nil {
					var cc CommandCenterConfig
					if diags := gohcl.DecodeBody(block.Body, nil, &cc); !diags.HasErrors() {
						cc.Defaults()
						partial.CommandCenter = &cc
					}
				}
			case "mcp":
				if partial.MCP == nil {
					var mc MCPConfig
					if diags := gohcl.DecodeBody(block.Body, nil, &mc); !diags.HasErrors() {
						mc.Defaults()
						partial.MCP = &mc
					}
				}
			}
		}
	}

	// Best-effort: try loading plugin binaries so their tools are visible
	partial.LoadedPlugins = make(map[string]*plugin.PluginClient)
	for _, p := range partial.Plugins {
		if p.Version == "" {
			continue
		}
		client, loadErr := plugin.LoadPlugin(p.Name, p.Version, p.Source)
		if loadErr != nil {
			continue
		}
		partial.LoadedPlugins[p.Name] = client
	}

	return partial, err
}

// resolveConfigFiles returns the list of .hcl files for a given path.
func resolveConfigFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var files []string
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") {
			files = append(files, filepath.Join(path, e.Name()))
		}
	}
	return files, nil
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

	if c.CommandCenter != nil {
		if err := c.CommandCenter.Validate(); err != nil {
			return fmt.Errorf("command_center: %w", err)
		}
	}

	if c.MCP != nil {
		if err := c.MCP.Validate(); err != nil {
			return fmt.Errorf("mcp: %w", err)
		}
	}

	for _, a := range c.Agents {
		if err := a.Validate(); err != nil {
			return err
		}
	}

	for _, fb := range c.SharedFolders {
		if err := fb.Validate(); err != nil {
			return fmt.Errorf("shared_folder '%s': %w", fb.Name, err)
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
	// Format: builtins.{namespace}.{tool} for built-in tools
	//         plugins.{namespace}.{tool} for external plugins
	//         tools.{name} for custom tools
	validToolRefs := make(map[string]bool)

	// Add built-in tools (builtins.http.get, builtins.http.get, etc.)
	for namespace, tools := range BuiltinTools {
		for _, toolName := range tools {
			validToolRefs[fmt.Sprintf("builtins.%s.%s", namespace, toolName)] = true
		}
		// Add "all" marker for loading all tools from this builtin namespace
		validToolRefs[fmt.Sprintf("builtins.%s.all", namespace)] = true
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

	// Validate tool references in mission-scoped agents
	for _, m := range c.Missions {
		for _, a := range m.LocalAgents {
			for _, toolRef := range a.Tools {
				if !validToolRefs[toolRef] {
					return fmt.Errorf("mission '%s' agent '%s': unknown tool '%s'. Available tools: %v", m.Name, a.Name, toolRef, getToolNames(validToolRefs))
				}
			}
		}
	}

	// Validate global skills
	for _, s := range c.Skills {
		if err := s.Validate(); err != nil {
			return fmt.Errorf("skill '%s': %w", s.Name, err)
		}
		for _, toolRef := range s.Tools {
			if !validToolRefs[toolRef] {
				return fmt.Errorf("skill '%s': unknown tool '%s'", s.Name, toolRef)
			}
		}
	}

	// Build global skill names set for validation
	globalSkillNames := make(map[string]bool)
	for _, s := range c.Skills {
		globalSkillNames[s.Name] = true
	}

	// Validate agent skill references and agent-scoped skills
	for _, a := range c.Agents {
		for _, skillRef := range a.Skills {
			name := strings.TrimPrefix(skillRef, "skills.")
			if !globalSkillNames[name] {
				return fmt.Errorf("agent '%s': unknown skill '%s'", a.Name, name)
			}
		}
		for _, ls := range a.LocalSkills {
			if err := ls.Validate(); err != nil {
				return fmt.Errorf("agent '%s' skill '%s': %w", a.Name, ls.Name, err)
			}
			if globalSkillNames[ls.Name] {
				return fmt.Errorf("agent '%s': agent-scoped skill '%s' conflicts with global skill of the same name", a.Name, ls.Name)
			}
			for _, toolRef := range ls.Tools {
				if !validToolRefs[toolRef] {
					return fmt.Errorf("agent '%s' skill '%s': unknown tool '%s'", a.Name, ls.Name, toolRef)
				}
			}
		}
	}

	// Validate mission-scoped agent skill references
	for _, m := range c.Missions {
		for _, a := range m.LocalAgents {
			for _, skillRef := range a.Skills {
				name := strings.TrimPrefix(skillRef, "skills.")
				if !globalSkillNames[name] {
					// Check if it's an agent-local skill
					found := false
					for _, ls := range a.LocalSkills {
						if ls.Name == name {
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("mission '%s' agent '%s': unknown skill '%s'", m.Name, a.Name, name)
					}
				}
			}
			for _, ls := range a.LocalSkills {
				if err := ls.Validate(); err != nil {
					return fmt.Errorf("mission '%s' agent '%s' skill '%s': %w", m.Name, a.Name, ls.Name, err)
				}
				if globalSkillNames[ls.Name] {
					return fmt.Errorf("mission '%s' agent '%s': agent-scoped skill '%s' conflicts with global skill", m.Name, a.Name, ls.Name)
				}
				for _, toolRef := range ls.Tools {
					if !validToolRefs[toolRef] {
						return fmt.Errorf("mission '%s' agent '%s' skill '%s': unknown tool '%s'", m.Name, a.Name, ls.Name, toolRef)
					}
				}
			}
		}
	}

	// Build mission names set for cross-mission route validation
	allMissionNames := make(map[string]bool, len(c.Missions))
	for _, m := range c.Missions {
		allMissionNames[m.Name] = true
	}

	// Validate missions
	for i := range c.Missions {
		if err := c.Missions[i].Validate(c.Models, c.Agents, c.SharedFolders, allMissionNames); err != nil {
			return fmt.Errorf("mission '%s': %w", c.Missions[i].Name, err)
		}
	}

	// Validate webhook path uniqueness across all missions
	webhookPaths := make(map[string]string) // path → mission name
	for _, m := range c.Missions {
		if m.Trigger == nil {
			continue
		}
		path := m.Trigger.WebhookPath
		if other, exists := webhookPaths[path]; exists {
			return fmt.Errorf("mission '%s': webhook_path %q conflicts with mission '%s'", m.Name, path, other)
		}
		webhookPaths[path] = m.Name
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
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".hcl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .hcl config files found in %q — add at least one .hcl file with your configuration", dir)
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
	Missions  []*hcl.Block
	Storage       []*hcl.Block
	CommandCenter []*hcl.Block
	SharedFolders []*hcl.Block
	MCP           []*hcl.Block
	Skills        []*hcl.Block
}

// loadFromFiles implements staged loading: variables → models → agents → tools
func loadFromFiles(files []string) (*Config, error) {
	// Build config functions map: schema helpers + load()
	// Set package-level configFuncs so buildXxxContext helpers can access it
	configDir := filepath.Dir(files[0])
	configFuncs = schemafunc.SchemaFunctions()
	configFuncs["load"] = schemafunc.MakeLoadFunc(configDir)

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
				{Type: "mission", LabelNames: []string{"name"}},
				{Type: "storage"},
				{Type: "command_center"},
				{Type: "shared_folder", LabelNames: []string{"name"}},
				{Type: "mcp"},
				{Type: "skill", LabelNames: []string{"name"}},
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
			case "mission":
				pb.Missions = append(pb.Missions, block)
			case "storage":
				pb.Storage = append(pb.Storage, block)
			case "command_center":
				pb.CommandCenter = append(pb.CommandCenter, block)
			case "shared_folder":
				pb.SharedFolders = append(pb.SharedFolders, block)
			case "mcp":
				pb.MCP = append(pb.MCP, block)
			case "skill":
				pb.Skills = append(pb.Skills, block)
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

	// Parse storage block (required)
	var storageConfig StorageConfig
	hasStorage := false
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Storage {
			hasStorage = true
			diags := gohcl.DecodeBody(block.Body, varsCtx, &storageConfig)
			if diags.HasErrors() {
				return nil, fmt.Errorf("storage: %w", diags)
			}
		}
	}
	if !hasStorage {
		return nil, fmt.Errorf("a storage block is required in the configuration, e.g.:\n\n  storage {\n    backend = \"sqlite\"\n  }\n")
	}
	storageConfig.Defaults()

	// When SQUADRON_HOME is set, default SQLite path goes there instead of config dir
	if storageConfig.Path == ".squadron/store.db" {
		if sqHome, err := paths.SquadronHome(); err == nil && os.Getenv("SQUADRON_HOME") != "" {
			storageConfig.Path = filepath.Join(sqHome, "store.db")
		}
	}

	// Resolve relative SQLite path against config directory
	if storageConfig.Backend == "sqlite" && !filepath.IsAbs(storageConfig.Path) && len(files) > 0 {
		configDir := filepath.Dir(files[0])
		storageConfig.Path = filepath.Join(configDir, storageConfig.Path)
	}

	// Parse command_center block (optional singleton, with vars context)
	var commandCenterConfig *CommandCenterConfig
	for _, pb := range allParsedBlocks {
		for _, block := range pb.CommandCenter {
			var cc CommandCenterConfig
			diags := gohcl.DecodeBody(block.Body, varsCtx, &cc)
			if diags.HasErrors() {
				return nil, fmt.Errorf("command_center: %w", diags)
			}
			cc.Defaults()
			commandCenterConfig = &cc
		}
	}

	// parseModelBlock parses a model block with optional pricing sub-blocks.
	parseModelBlock := func(block *hcl.Block, ctx *hcl.EvalContext) (*Model, error) {
		content, _, diags := block.Body.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{
				{Name: "provider", Required: true},
				{Name: "allowed_models"},
				{Name: "aliases"},
				{Name: "api_key"},
				{Name: "base_url"},
				{Name: "prompt_caching"},
			},
			Blocks: []hcl.BlockHeaderSchema{
				{Type: "pricing", LabelNames: []string{"model"}},
			},
		})
		if diags.HasErrors() {
			return nil, diags
		}

		m := &Model{Name: block.Labels[0]}

		providerVal, d := content.Attributes["provider"].Expr.Value(ctx)
		if d.HasErrors() {
			return nil, d
		}
		m.Provider = Provider(providerVal.AsString())

		if attr, ok := content.Attributes["allowed_models"]; ok {
			modelsVal, d := attr.Expr.Value(ctx)
			if d.HasErrors() {
				return nil, d
			}
			for it := modelsVal.ElementIterator(); it.Next(); {
				_, v := it.Element()
				m.AllowedModels = append(m.AllowedModels, v.AsString())
			}
		}

		if attr, ok := content.Attributes["aliases"]; ok {
			aliasesVal, d := attr.Expr.Value(ctx)
			if d.HasErrors() {
				return nil, d
			}
			m.Aliases = make(map[string]string)
			for it := aliasesVal.ElementIterator(); it.Next(); {
				k, v := it.Element()
				m.Aliases[k.AsString()] = v.AsString()
			}
		}

		if attr, ok := content.Attributes["api_key"]; ok {
			keyVal, d := attr.Expr.Value(ctx)
			if d.HasErrors() {
				return nil, d
			}
			m.APIKey = keyVal.AsString()
		}

		if attr, ok := content.Attributes["base_url"]; ok {
			urlVal, d := attr.Expr.Value(ctx)
			if d.HasErrors() {
				return nil, d
			}
			m.BaseURL = urlVal.AsString()
		}

		if attr, ok := content.Attributes["prompt_caching"]; ok {
			val, d := attr.Expr.Value(ctx)
			if d.HasErrors() {
				return nil, d
			}
			b := val.True()
			m.PromptCaching = &b
		}

		// Parse pricing sub-blocks
		for _, pBlock := range content.Blocks {
			if pBlock.Type != "pricing" {
				continue
			}
			modelName := pBlock.Labels[0]
			var pc ModelPricingConfig
			pDiags := gohcl.DecodeBody(pBlock.Body, ctx, &pc)
			if pDiags.HasErrors() {
				return nil, fmt.Errorf("pricing '%s': %w", modelName, pDiags)
			}
			if m.Pricing == nil {
				m.Pricing = make(map[string]*ModelPricingConfig)
			}
			m.Pricing[modelName] = &pc
		}

		return m, nil
	}

	// Parse mcp block (optional singleton, with vars context)
	var mcpConfig *MCPConfig
	for _, pb := range allParsedBlocks {
		for _, block := range pb.MCP {
			var mc MCPConfig
			diags := gohcl.DecodeBody(block.Body, varsCtx, &mc)
			if diags.HasErrors() {
				return nil, fmt.Errorf("mcp: %w", diags)
			}
			mc.Defaults()
			mcpConfig = &mc
		}
	}

	// Parse shared_folder blocks (optional, with vars context)
	var allSharedFolders []SharedFolder
	for _, pb := range allParsedBlocks {
		for _, block := range pb.SharedFolders {
			var fb SharedFolder
			fb.Name = block.Labels[0]
			diags := gohcl.DecodeBody(block.Body, varsCtx, &fb)
			if diags.HasErrors() {
				return nil, fmt.Errorf("shared_folder '%s': %w", fb.Name, diags)
			}
			allSharedFolders = append(allSharedFolders, fb)
		}
	}

	// Stage 1.5: Load plugins (with vars context - plugins are simple, load early so tools can reference them)
	var allPlugins []Plugin
	loadedPlugins := make(map[string]*plugin.PluginClient)

	for _, pb := range allParsedBlocks {
		for _, block := range pb.Plugins {
			p, err := parsePluginBlock(block, varsCtx)
			if err != nil {
				return nil, err
			}
			allPlugins = append(allPlugins, *p)

			// Load the plugin (passes source for auto-download if not found locally)
			client, err := plugin.LoadPlugin(p.Name, p.Version, p.Source)
			if err != nil {
				return nil, fmt.Errorf("plugin '%s' (version %s) failed to load: %w", p.Name, p.Version, err)
			}
			// Add to loaded plugins first — even if Configure fails,
			// the plugin binary is running and ListTools works for metadata
			loadedPlugins[p.Name] = client

			// Configure the plugin with settings if any
			if len(p.Settings) > 0 {
				// Check for empty settings before calling Configure — plugins may
				// crash on invalid input, killing the gRPC connection
				var emptySettings []string
				for k, v := range p.Settings {
					if v == "" {
						emptySettings = append(emptySettings, k)
					}
				}
				if len(emptySettings) > 0 {
					return nil, fmt.Errorf("plugin '%s' failed to configure: settings %v are empty — check that the corresponding variables are set", p.Name, emptySettings)
				}
				if err := client.Configure(p.Settings); err != nil {
					return nil, fmt.Errorf("plugin '%s' failed to configure: %w", p.Name, err)
				}
			}
		}
	}

	// Build plugins context for HCL evaluation
	pluginsCtx := buildPluginsContext(varsCtx, loadedPlugins)

	// Stage 2: Load models (with vars + plugins context)
	var allModels []Model
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Models {
			m, err := parseModelBlock(block, pluginsCtx)
			if err != nil {
				return nil, fmt.Errorf("model '%s': %w", block.Labels[0], err)
			}
			allModels = append(allModels, *m)
		}
	}

	// Build models context (add to plugins context)
	modelsCtx := buildModelsContext(pluginsCtx, allModels)

	// Stage 3: Load custom tools (with vars + models + plugins context, plus dynamic field parsing)
	// Wrap with schema functions so tools can use inputs = { field = string(...) } shorthand.
	toolSchemaCtx := modelsCtx.NewChild()
	toolSchemaCtx.Variables = schemafunc.SchemaTypeVars()
	toolSchemaCtx.Functions = configFuncs
	var allTools []CustomTool
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Tools {
			tool, err := parseToolBlock(block, toolSchemaCtx, loadedPlugins)
			if err != nil {
				return nil, err
			}
			allTools = append(allTools, *tool)
		}
	}

	// Build tools context (add to models context) - includes both internal and custom tools
	fullCtx := buildToolsContext(modelsCtx, allTools)

	// Stage 3.5: Load global skills (with full context so tool refs resolve)
	var allSkills []Skill
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Skills {
			s, err := parseSkillBlock(block, fullCtx)
			if err != nil {
				return nil, err
			}
			allSkills = append(allSkills, *s)
		}
	}

	// Build skills context (adds skills.X namespace)
	skillsCtx := buildSkillsContext(fullCtx, allSkills)

	// Stage 4: Load agents (with vars + models + tools + skills context)
	var allAgents []Agent
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Agents {
			a, err := parseAgentBlock(block, skillsCtx)
			if err != nil {
				return nil, err
			}
			allAgents = append(allAgents, *a)
		}
	}

	// Build agents context (add to full context)
	agentsCtx := buildAgentsContext(skillsCtx, allAgents)

	// Add shared_folders namespace for mission references
	if len(allSharedFolders) > 0 {
		folderMap := make(map[string]cty.Value)
		for _, f := range allSharedFolders {
			folderMap[f.Name] = cty.StringVal(f.Name)
		}
		agentsCtx.Variables["shared_folders"] = cty.ObjectVal(folderMap)
	}

	// Stage 5: Load missions (with vars + models + tools + agents + shared_folders context)
	// First pass: collect all mission names so router targets can reference missions.*
	missionNames := make(map[string]cty.Value)
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Missions {
			if len(block.Labels) > 0 {
				missionNames[block.Labels[0]] = cty.StringVal(block.Labels[0])
			}
		}
	}
	missionsCtx := &hcl.EvalContext{
		Variables: make(map[string]cty.Value),
		Functions: configFuncs,
	}
	for k, v := range agentsCtx.Variables {
		missionsCtx.Variables[k] = v
	}
	// Schema type vars (string, number, etc.) must be in the Variables map so
	// bare references like list(string, "desc") resolve correctly.
	for k, v := range schemafunc.SchemaTypeVars() {
		missionsCtx.Variables[k] = v
	}
	if len(missionNames) > 0 {
		missionsCtx.Variables["missions"] = cty.ObjectVal(missionNames)
	}

	// Second pass: parse missions with missions context available
	var allMissions []Mission
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Missions {
			mission, err := parseMissionBlock(block, missionsCtx)
			if err != nil {
				return nil, err
			}
			allMissions = append(allMissions, *mission)
		}
	}

	return &Config{
		Variables:      allVars,
		Models:         allModels,
		Agents:         allAgents,
		CustomTools:    allTools,
		Plugins:        allPlugins,
		Missions:      allMissions,
		Skills:         allSkills,
		Storage:        &storageConfig,
		CommandCenter:  commandCenterConfig,
		MCP:            mcpConfig,
		SharedFolders:   allSharedFolders,
		LoadedPlugins:  loadedPlugins,
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

	// Parse the tool block content: static fields (implements, description) + inputs (block or attribute) + dynamic fields
	toolContent, remainBody, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "implements", Required: true},
			{Name: "description"},
			{Name: "inputs"}, // shorthand: inputs = { city = string("desc", true) }
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "inputs"}, // verbose: inputs { field "city" { ... } }
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

	// Parse inputs — accept either shorthand attribute or verbose block form.
	if inputsAttr, ok := toolContent.Attributes["inputs"]; ok {
		// Shorthand: inputs = { city = string("The target city", true) }
		val, diags := inputsAttr.Expr.Value(baseCtx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("tool '%s': inputs: %w", toolName, diags)
		}
		fields, err := parseSchemaObject(val)
		if err != nil {
			return nil, fmt.Errorf("tool '%s': inputs: %w", toolName, err)
		}
		tool.Inputs = &InputsSchema{Fields: fields}
	} else {
		// Verbose block form: inputs { field "city" { type = "string" ... } }
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

	// Seed schema type-ref variables (string, number, integer, bool) so they can be
	// used as bare type arguments inside list()/map(): e.g. list(string, "Tags").
	// Subsequent context builders copy Variables, so these propagate automatically.
	baseVars := map[string]cty.Value{
		"vars": cty.ObjectVal(varsMap),
	}
	for k, v := range schemafunc.SchemaTypeVars() {
		baseVars[k] = v
	}

	return &hcl.EvalContext{
		Variables: baseVars,
		Functions: configFuncs,
	}, varsMap
}

// buildModelsContext adds models to existing context
func buildModelsContext(ctx *hcl.EvalContext, models []Model) *hcl.EvalContext {
	modelsMap := make(map[string]cty.Value)
	for _, m := range models {
		providerModels := make(map[string]cty.Value)
		for key := range m.AvailableModels() {
			providerModels[key] = cty.StringVal(key)
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
		Functions: configFuncs,
	}
}

// buildToolsContext adds tools namespace to existing context (custom tools only)
// Built-in tools are in the builtins namespace (builtins.http.get, builtins.http.get)
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
		Functions: configFuncs,
	}
}

// buildPluginsContext adds builtins and plugins namespaces to existing context
// Creates builtins.{namespace}.{tool} references for built-in tools
// Creates plugins.{plugin_name}.{tool_name} references for external plugins
func buildPluginsContext(ctx *hcl.EvalContext, loadedPlugins map[string]*plugin.PluginClient) *hcl.EvalContext {
	// Build builtins namespace (bash, http, dataset, utils)
	builtinsMap := make(map[string]cty.Value)
	for namespace, tools := range BuiltinTools {
		toolsMap := make(map[string]cty.Value)
		for _, toolName := range tools {
			toolsMap[toolName] = cty.StringVal(fmt.Sprintf("builtins.%s.%s", namespace, toolName))
		}
		toolsMap["all"] = cty.StringVal(fmt.Sprintf("builtins.%s.all", namespace))
		builtinsMap[namespace] = cty.ObjectVal(toolsMap)
	}

	// Build plugins namespace (external plugins only)
	pluginsMap := make(map[string]cty.Value)
	for pluginName, client := range loadedPlugins {
		toolsMap := make(map[string]cty.Value)
		tools, err := client.ListTools()
		if err == nil {
			for _, t := range tools {
				toolsMap[t.Name] = cty.StringVal(fmt.Sprintf("plugins.%s.%s", pluginName, t.Name))
			}
		}
		toolsMap["all"] = cty.StringVal(fmt.Sprintf("plugins.%s.all", pluginName))
		pluginsMap[pluginName] = cty.ObjectVal(toolsMap)
	}

	// Copy existing vars and add both namespaces
	newVars := make(map[string]cty.Value)
	for k, v := range ctx.Variables {
		newVars[k] = v
	}
	newVars["builtins"] = cty.ObjectVal(builtinsMap)
	if len(pluginsMap) > 0 {
		newVars["plugins"] = cty.ObjectVal(pluginsMap)
	}

	return &hcl.EvalContext{
		Variables: newVars,
		Functions: configFuncs,
	}
}

// GetPluginTool returns a plugin tool by its implements string (e.g., "plugins.pinger.echo")
func (c *Config) GetPluginTool(implements string) (*plugin.PluginClient, string, error) {
	parts := strings.Split(implements, ".")
	if len(parts) != 3 || (parts[0] != "plugins" && parts[0] != "builtins") {
		return nil, "", fmt.Errorf("invalid tool reference: %s", implements)
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
func buildSkillsContext(ctx *hcl.EvalContext, skills []Skill) *hcl.EvalContext {
	if len(skills) == 0 {
		return ctx
	}
	skillsMap := make(map[string]cty.Value)
	for _, s := range skills {
		skillsMap[s.Name] = cty.StringVal(fmt.Sprintf("skills.%s", s.Name))
	}

	newVars := make(map[string]cty.Value)
	for k, v := range ctx.Variables {
		newVars[k] = v
	}
	newVars["skills"] = cty.ObjectVal(skillsMap)

	return &hcl.EvalContext{
		Variables: newVars,
		Functions: configFuncs,
	}
}

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
		Functions: configFuncs,
	}
}

// missionTaskBlock is used for parsing task blocks within a mission
type missionTaskBlock struct {
	Name      string   `hcl:"name,label"`
	Objective string   `hcl:"objective"`
	Agents    []string `hcl:"agents"`
	DependsOn []string `hcl:"depends_on,optional"`
}

// parseMissionBlock parses a mission block with its nested task blocks
// parseAgentBlock parses an agent HCL block into an Agent struct.
// Used for both global agents and mission-scoped agents.
func parseSkillBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Skill, error) {
	var s Skill
	s.Name = block.Labels[0]
	diags := gohcl.DecodeBody(block.Body, ctx, &s)
	if diags.HasErrors() {
		return nil, fmt.Errorf("skill '%s': %w", s.Name, diags)
	}
	return &s, nil
}

func parseAgentBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Agent, error) {
	// Use PartialContent to split the agent body into known parts.
	// gohcl cannot handle labeled sub-blocks (skill "name" {}) so we parse manually.
	content, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "model", Required: true},
			{Name: "personality", Required: true},
			{Name: "role", Required: true},
			{Name: "tools"},
			{Name: "skills"},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "skill", LabelNames: []string{"name"}},
			{Type: "pruning"},
			{Type: "compaction"},
			{Type: "tool_response"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("agent '%s': %w", block.Labels[0], diags)
	}

	// Parse agent-scoped skill blocks first (needed for skills context)
	var localSkills []Skill
	for _, b := range content.Blocks {
		if b.Type != "skill" {
			continue
		}
		s, err := parseSkillBlock(b, ctx)
		if err != nil {
			return nil, fmt.Errorf("agent '%s': %w", block.Labels[0], err)
		}
		localSkills = append(localSkills, *s)
	}

	// Build augmented context with local skill names
	agentCtx := ctx
	if len(localSkills) > 0 {
		skillsMap := make(map[string]cty.Value)
		if existing, ok := ctx.Variables["skills"]; ok && existing.Type().IsObjectType() {
			for k, v := range existing.AsValueMap() {
				skillsMap[k] = v
			}
		}
		for _, s := range localSkills {
			skillsMap[s.Name] = cty.StringVal(fmt.Sprintf("skills.%s", s.Name))
		}
		newVars := make(map[string]cty.Value)
		for k, v := range ctx.Variables {
			newVars[k] = v
		}
		newVars["skills"] = cty.ObjectVal(skillsMap)
		agentCtx = &hcl.EvalContext{
			Variables: newVars,
			Functions: configFuncs,
		}
	}

	a := &Agent{Name: block.Labels[0], LocalSkills: localSkills}

	// Decode attributes
	if attr, ok := content.Attributes["model"]; ok {
		val, d := attr.Expr.Value(agentCtx)
		if d.HasErrors() {
			return nil, fmt.Errorf("agent '%s' model: %w", a.Name, d)
		}
		a.Model = val.AsString()
	}
	if attr, ok := content.Attributes["personality"]; ok {
		val, d := attr.Expr.Value(agentCtx)
		if d.HasErrors() {
			return nil, fmt.Errorf("agent '%s' personality: %w", a.Name, d)
		}
		a.Personality = val.AsString()
	}
	if attr, ok := content.Attributes["role"]; ok {
		val, d := attr.Expr.Value(agentCtx)
		if d.HasErrors() {
			return nil, fmt.Errorf("agent '%s' role: %w", a.Name, d)
		}
		a.Role = val.AsString()
	}
	if attr, ok := content.Attributes["tools"]; ok {
		val, d := attr.Expr.Value(agentCtx)
		if d.HasErrors() {
			return nil, fmt.Errorf("agent '%s' tools: %w", a.Name, d)
		}
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			a.Tools = append(a.Tools, v.AsString())
		}
	}
	if attr, ok := content.Attributes["skills"]; ok {
		val, d := attr.Expr.Value(agentCtx)
		if d.HasErrors() {
			return nil, fmt.Errorf("agent '%s' skills: %w", a.Name, d)
		}
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			a.Skills = append(a.Skills, v.AsString())
		}
	}

	// Decode sub-blocks
	for _, b := range content.Blocks {
		switch b.Type {
		case "pruning":
			var p Pruning
			d := gohcl.DecodeBody(b.Body, agentCtx, &p)
			if d.HasErrors() {
				return nil, fmt.Errorf("agent '%s' pruning: %w", a.Name, d)
			}
			a.Pruning = &p
		case "compaction":
			var c Compaction
			d := gohcl.DecodeBody(b.Body, agentCtx, &c)
			if d.HasErrors() {
				return nil, fmt.Errorf("agent '%s' compaction: %w", a.Name, d)
			}
			a.Compaction = &c
		case "tool_response":
			var tr ToolResponseConfig
			d := gohcl.DecodeBody(b.Body, agentCtx, &tr)
			if d.HasErrors() {
				return nil, fmt.Errorf("agent '%s' tool_response: %w", a.Name, d)
			}
			a.ToolResponse = &tr
		}
	}

	return a, nil
}

func parseMissionBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Mission, error) {
	missionName := block.Labels[0]

	// Parse the mission block content
	missionContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "agents", Required: true},
			{Name: "directive"},
			{Name: "folders"},
			{Name: "max_parallel"},
			{Name: "inputs"}, // shorthand: inputs = { field = string("desc", { default = "val" }) }
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "commander"},
			{Type: "agent", LabelNames: []string{"name"}}, // mission-scoped agents
			{Type: "task", LabelNames: []string{"name"}},
			{Type: "input", LabelNames: []string{"name"}}, // verbose input blocks still supported
			{Type: "dataset", LabelNames: []string{"name"}},
			{Type: "secret", LabelNames: []string{"name"}},
			{Type: "folder"},
			{Type: "schedule"},
			{Type: "trigger"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("mission '%s': %w", missionName, diags)
	}

	// Parse commander block (required)
	var missionCommander *MissionCommander
	for _, cmdBlock := range missionContent.Blocks {
		if cmdBlock.Type != "commander" {
			continue
		}
		if missionCommander != nil {
			return nil, fmt.Errorf("mission '%s': only one commander block allowed", missionName)
		}

		// Parse commander block content
		cmdContent, _, cmdDiags := cmdBlock.Body.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{
				{Name: "model", Required: true},
			},
			Blocks: []hcl.BlockHeaderSchema{
				{Type: "compaction"},
				{Type: "pruning"},
				{Type: "tool_response"},
			},
		})
		if cmdDiags.HasErrors() {
			return nil, fmt.Errorf("mission '%s' commander: %w", missionName, cmdDiags)
		}

		// Get model attribute
		modelAttr := cmdContent.Attributes["model"]
		modelVal, modelDiags := modelAttr.Expr.Value(ctx)
		if modelDiags.HasErrors() {
			return nil, fmt.Errorf("mission '%s' commander model: %w", missionName, modelDiags)
		}

		missionCommander = &MissionCommander{
			Model: modelVal.AsString(),
		}

		// Parse optional compaction and pruning sub-blocks
		for _, subBlock := range cmdContent.Blocks {
			switch subBlock.Type {
			case "compaction":
				var comp Compaction
				compDiags := gohcl.DecodeBody(subBlock.Body, ctx, &comp)
				if compDiags.HasErrors() {
					return nil, fmt.Errorf("mission '%s' commander compaction: %w", missionName, compDiags)
				}
				// Default turn_retention to 10 when compaction is enabled
				if comp.TurnRetention <= 0 {
					comp.TurnRetention = 10
				}
				missionCommander.Compaction = &comp
			case "pruning":
				var pruning CommanderPruning
				pruningDiags := gohcl.DecodeBody(subBlock.Body, ctx, &pruning)
				if pruningDiags.HasErrors() {
					return nil, fmt.Errorf("mission '%s' commander pruning: %w", missionName, pruningDiags)
				}
				missionCommander.Pruning = &pruning
			case "tool_response":
				var tr ToolResponseConfig
				trDiags := gohcl.DecodeBody(subBlock.Body, ctx, &tr)
				if trDiags.HasErrors() {
					return nil, fmt.Errorf("mission '%s' commander tool_response: %w", missionName, trDiags)
				}
				missionCommander.ToolResponse = &tr
			}
		}
	}
	if missionCommander == nil {
		return nil, fmt.Errorf("mission '%s': commander block is required", missionName)
	}

	// Parse mission-scoped agent blocks (before resolving agents attribute)
	var localAgents []Agent
	for _, agentBlock := range missionContent.Blocks {
		if agentBlock.Type != "agent" {
			continue
		}
		a, err := parseAgentBlock(agentBlock, ctx)
		if err != nil {
			return nil, fmt.Errorf("mission '%s': %w", missionName, err)
		}
		localAgents = append(localAgents, *a)
	}

	// Build mission-local context that includes scoped agent names in the agents namespace
	missionCtx := ctx
	if len(localAgents) > 0 {
		agentsMap := make(map[string]cty.Value)
		// Copy existing global agents
		if existingAgents, ok := ctx.Variables["agents"]; ok && existingAgents.Type().IsObjectType() {
			for k, v := range existingAgents.AsValueMap() {
				agentsMap[k] = v
			}
		}
		// Add local agent names
		for _, a := range localAgents {
			agentsMap[a.Name] = cty.StringVal(a.Name)
		}
		newVars := make(map[string]cty.Value)
		for k, v := range ctx.Variables {
			newVars[k] = v
		}
		newVars["agents"] = cty.ObjectVal(agentsMap)
		missionCtx = &hcl.EvalContext{
			Variables: newVars,
			Functions: ctx.Functions,
		}
	}

	// Get agents attribute (mission-level agents) — use mission context so local agents resolve
	agentsAttr := missionContent.Attributes["agents"]
	agentsVal, diags := agentsAttr.Expr.Value(missionCtx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("mission '%s': %w", missionName, diags)
	}

	var missionAgents []string
	for it := agentsVal.ElementIterator(); it.Next(); {
		_, v := it.Element()
		missionAgents = append(missionAgents, v.AsString())
	}

	var directive string
	if attr, ok := missionContent.Attributes["directive"]; ok {
		val, diags := attr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("mission '%s' directive: %w", missionName, diags)
		}
		directive = val.AsString()
	}

	// Parse optional folders attribute (list of shared folder names)
	var missionFolders []string
	if foldersAttr, ok := missionContent.Attributes["folders"]; ok {
		foldersVal, diags := foldersAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("mission '%s' folders: %w", missionName, diags)
		}
		for it := foldersVal.ElementIterator(); it.Next(); {
			_, v := it.Element()
			missionFolders = append(missionFolders, v.AsString())
		}
	}

	// Parse optional folder block (dedicated mission folder)
	var missionFolder *MissionFolder
	for _, folderBlock := range missionContent.Blocks {
		if folderBlock.Type != "folder" {
			continue
		}
		if missionFolder != nil {
			return nil, fmt.Errorf("mission '%s': only one folder block allowed", missionName)
		}
		var mf MissionFolder
		diags := gohcl.DecodeBody(folderBlock.Body, ctx, &mf)
		if diags.HasErrors() {
			return nil, fmt.Errorf("mission '%s' folder: %w", missionName, diags)
		}
		missionFolder = &mf
	}

	// Parse schedule blocks (optional, multiple allowed)
	var schedules []Schedule
	for _, schedBlock := range missionContent.Blocks {
		if schedBlock.Type != "schedule" {
			continue
		}
		sched, err := parseScheduleBlock(schedBlock, ctx)
		if err != nil {
			return nil, fmt.Errorf("mission '%s' schedule: %w", missionName, err)
		}
		schedules = append(schedules, *sched)
	}

	// Parse trigger block (optional, singleton)
	var trigger *Trigger
	for _, trigBlock := range missionContent.Blocks {
		if trigBlock.Type != "trigger" {
			continue
		}
		if trigger != nil {
			return nil, fmt.Errorf("mission '%s': only one trigger block allowed", missionName)
		}
		var t Trigger
		diags := gohcl.DecodeBody(trigBlock.Body, ctx, &t)
		if diags.HasErrors() {
			return nil, fmt.Errorf("mission '%s' trigger: %w", missionName, diags)
		}
		trigger = &t
	}

	// Parse max_parallel attribute (optional, default 3)
	maxParallel := 3
	if attr, ok := missionContent.Attributes["max_parallel"]; ok {
		val, diags := attr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("mission '%s' max_parallel: %w", missionName, diags)
		}
		bf := val.AsBigFloat()
		mp, _ := bf.Int64()
		maxParallel = int(mp)
	}

	mission := &Mission{
		Name:        missionName,
		Directive:   directive,
		Commander:   missionCommander,
		Agents:      missionAgents,
		LocalAgents: localAgents,
		Folders:     missionFolders,
		Folder:      missionFolder,
		Schedules:   schedules,
		Trigger:     trigger,
		MaxParallel: maxParallel,
	}

	// Parse inputs — accept either shorthand attribute or verbose labeled block form.
	if inputsAttr, ok := missionContent.Attributes["inputs"]; ok {
		// Shorthand: inputs = { severity = string("Severity level", { default = "high" }) }
		val, diags := inputsAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("mission '%s': inputs: %w", missionName, diags)
		}
		parsedInputs, err := parseSchemaObjectAsMissionInputs(val)
		if err != nil {
			return nil, fmt.Errorf("mission '%s': inputs: %w", missionName, err)
		}
		mission.Inputs = append(mission.Inputs, parsedInputs...)
	} else {
		// Verbose form: input "severity" { type = "string"; description = "..."; default = "high" }
		for _, inputBlock := range missionContent.Blocks {
			if inputBlock.Type != "input" {
				continue
			}
			input, err := parseMissionInputBlock(inputBlock, ctx)
			if err != nil {
				return nil, fmt.Errorf("mission '%s': %w", missionName, err)
			}
			mission.Inputs = append(mission.Inputs, *input)
		}
	}

	// Build inputs context with placeholder values for dataset bind_to validation
	inputsType := mission.BuildInputsCtyType()
	inputsCtx := &hcl.EvalContext{
		Variables: make(map[string]cty.Value),
		Functions: configFuncs,
	}
	for k, v := range ctx.Variables {
		inputsCtx.Variables[k] = v
	}
	inputsCtx.Variables["inputs"] = cty.UnknownVal(inputsType)

	// Parse dataset blocks
	for _, datasetBlock := range missionContent.Blocks {
		if datasetBlock.Type != "dataset" {
			continue
		}
		dataset, err := parseDatasetBlock(datasetBlock, inputsCtx)
		if err != nil {
			return nil, fmt.Errorf("mission '%s': %w", missionName, err)
		}
		mission.Datasets = append(mission.Datasets, *dataset)
	}

	// Build tasks context for depends_on references
	taskNames := make(map[string]cty.Value)
	for _, taskBlock := range missionContent.Blocks {
		if taskBlock.Type == "task" {
			taskNames[taskBlock.Labels[0]] = cty.StringVal(taskBlock.Labels[0])
		}
	}

	// Build datasets context for iterator references
	datasetNames := make(map[string]cty.Value)
	for _, ds := range mission.Datasets {
		datasetNames[ds.Name] = cty.StringVal(ds.Name)
	}

	// Add tasks, inputs, datasets, and item namespaces to context
	taskCtx := &hcl.EvalContext{
		Variables: make(map[string]cty.Value),
		Functions: configFuncs,
	}
	for k, v := range missionCtx.Variables {
		taskCtx.Variables[k] = v
	}
	taskCtx.Variables["tasks"] = cty.ObjectVal(taskNames)
	taskCtx.Variables["inputs"] = cty.UnknownVal(inputsType) // Placeholder for validation
	taskCtx.Variables["datasets"] = cty.ObjectVal(datasetNames)
	taskCtx.Variables["item"] = cty.DynamicVal // Placeholder for iteration item

	// Parse task blocks
	for _, taskBlock := range missionContent.Blocks {
		if taskBlock.Type != "task" {
			continue
		}

		task, err := parseTaskBlock(taskBlock, taskCtx)
		if err != nil {
			return nil, fmt.Errorf("mission '%s': %w", missionName, err)
		}
		mission.Tasks = append(mission.Tasks, *task)
	}

	return mission, nil
}

// parseScheduleBlock parses a schedule block, extracting known fields via gohcl
// and manually parsing the optional inputs map attribute.
func parseScheduleBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Schedule, error) {
	// Use PartialContent to extract the inputs attribute separately
	content, remain, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "inputs"},
		},
	})
	if diags.HasErrors() {
		return nil, diags
	}

	// Decode the remaining body (at, every, weekdays, cron, timezone) via gohcl
	var sched Schedule
	diags = gohcl.DecodeBody(remain, ctx, &sched)
	if diags.HasErrors() {
		return nil, diags
	}

	// Parse inputs attribute if present
	if attr, ok := content.Attributes["inputs"]; ok {
		val, valDiags := attr.Expr.Value(ctx)
		if valDiags.HasErrors() {
			return nil, valDiags
		}
		if !val.Type().IsObjectType() && !val.Type().IsMapType() {
			return nil, fmt.Errorf("'inputs' must be a map of strings")
		}
		sched.Inputs = make(map[string]string)
		for k, v := range val.AsValueMap() {
			if v.Type() != cty.String {
				return nil, fmt.Errorf("schedule input %q must be a string value", k)
			}
			sched.Inputs[k] = v.AsString()
		}
	}

	return &sched, nil
}

// parseMissionInputBlock parses an input block within a mission
func parseMissionInputBlock(block *hcl.Block, ctx *hcl.EvalContext) (*MissionInput, error) {
	inputName := block.Labels[0]

	inputContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "type", Required: true},
			{Name: "description"},
			{Name: "default"},
			{Name: "protected"},
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

	input := &MissionInput{
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

	// Get optional protected flag
	if protectedAttr, ok := inputContent.Attributes["protected"]; ok {
		protectedVal, diags := protectedAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("input '%s': %w", inputName, diags)
		}
		input.Protected = protectedVal.True()
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

// parseDatasetBlock parses a dataset block within a mission
func parseDatasetBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Dataset, error) {
	datasetName := block.Labels[0]

	datasetContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "description"},
			{Name: "bind_to"},
			{Name: "items"},
			{Name: "schema"}, // shorthand: schema = { field = string("desc", true) }
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "schema"}, // verbose: schema { field "name" { ... } }
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

	// Parse schema — accept either shorthand attribute or verbose block form.
	if schemaAttr, ok := datasetContent.Attributes["schema"]; ok {
		// Shorthand: schema = { id = number("Item ID", true) }
		val, diags := schemaAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("dataset '%s': schema: %w", datasetName, diags)
		}
		fields, err := parseSchemaObject(val)
		if err != nil {
			return nil, fmt.Errorf("dataset '%s': schema: %w", datasetName, err)
		}
		dataset.Schema = &InputsSchema{Fields: fields}
	} else {
		// Verbose block form: schema { field "id" { type = "number" ... } }
		for _, schemaBlock := range datasetContent.Blocks {
			if schemaBlock.Type == "schema" {
				schema, err := parseSchemaBlock(schemaBlock)
				if err != nil {
					return nil, fmt.Errorf("dataset '%s': %w", datasetName, err)
				}
				dataset.Schema = schema
			}
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

// parseTaskBlock parses a single task block within a mission
func parseTaskBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Task, error) {
	taskName := block.Labels[0]

	// Parse task attributes and blocks
	taskContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "objective", Required: true},
			{Name: "agents"},    // Optional - uses mission-level agents if not specified
			{Name: "depends_on"},
			{Name: "send_to"},
			{Name: "output"}, // shorthand: output = { field = string("desc", true) }
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "iterator"},
			{Type: "output"}, // verbose: output { field "name" { ... } }
			{Type: "router"},
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

	// Extract the raw objective text from source for display purposes
	rawObjective := extractExpressionSource(objectiveExpr)

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

	// Get send_to (optional array of task references)
	var sendTo []string
	if sendToAttr, ok := taskContent.Attributes["send_to"]; ok {
		sendToVal, diags := sendToAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("task '%s': %w", taskName, diags)
		}
		for it := sendToVal.ElementIterator(); it.Next(); {
			_, v := it.Element()
			sendTo = append(sendTo, v.AsString())
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

	// Parse output — accept either shorthand attribute or verbose block form.
	var output *OutputSchema
	if outputAttr, ok := taskContent.Attributes["output"]; ok {
		// Shorthand: output = { summary = string("Research summary", true) }
		val, diags := outputAttr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("task '%s': output: %w", taskName, diags)
		}
		fields, err := parseOutputSchemaObject(val)
		if err != nil {
			return nil, fmt.Errorf("task '%s': output: %w", taskName, err)
		}
		output = &OutputSchema{Fields: fields}
	} else {
		// Verbose block form: output { field "summary" { type = "string" ... } }
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
	}

	// Parse router block if present
	var router *TaskRouter
	for _, routerBlock := range taskContent.Blocks {
		if routerBlock.Type == "router" {
			r, err := parseRouterBlock(routerBlock, ctx)
			if err != nil {
				return nil, fmt.Errorf("task '%s': %w", taskName, err)
			}
			router = r
			break
		}
	}

	// Validate: sequential iterator tasks must not reference `item` in their objective.
	// The commander receives item data via the dataset_next tool, not through the objective.
	if iterator != nil && !iterator.Parallel {
		for _, traversal := range objectiveExpr.Variables() {
			if traversal.RootName() == "item" {
				return nil, fmt.Errorf("task '%s': sequential iterator tasks cannot reference 'item' in their objective — the commander receives each item via the dataset_next tool", taskName)
			}
		}
	}

	return &Task{
		Name:          taskName,
		ObjectiveExpr: objectiveExpr,
		RawObjective:  rawObjective,
		Agents:        agents,
		DependsOn:     dependsOn,
		SendTo:        sendTo,
		Iterator:      iterator,
		Output:        output,
		Router:        router,
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

// parseRouterBlock parses a router block containing route sub-blocks
func parseRouterBlock(block *hcl.Block, ctx *hcl.EvalContext) (*TaskRouter, error) {
	routerContent, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "route"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("router: %w", diags)
	}

	router := &TaskRouter{}
	for _, routeBlock := range routerContent.Blocks {
		if routeBlock.Type != "route" {
			continue
		}
		routeContent, _, diags := routeBlock.Body.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{
				{Name: "target", Required: true},
				{Name: "condition", Required: true},
			},
		})
		if diags.HasErrors() {
			return nil, fmt.Errorf("route: %w", diags)
		}

		targetVal, diags := routeContent.Attributes["target"].Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("route target: %w", diags)
		}
		conditionVal, diags := routeContent.Attributes["condition"].Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("route condition: %w", diags)
		}

		// Detect if the target references a mission (missions.foo) vs a task (tasks.foo)
		isMission := false
		for _, traversal := range routeContent.Attributes["target"].Expr.Variables() {
			if traversal.RootName() == "missions" {
				isMission = true
				break
			}
		}

		router.Routes = append(router.Routes, TaskRoute{
			Target:    targetVal.AsString(),
			Condition: conditionVal.AsString(),
			IsMission: isMission,
		})
	}

	return router, nil
}

// parsePluginBlock parses a plugin block with optional settings
func parsePluginBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Plugin, error) {
	pluginName := block.Labels[0]

	// Parse the plugin block content
	pluginContent, remainBody, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "source"},
			{Name: "version", Required: true},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "settings"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("plugin '%s': %w", pluginName, diags)
	}

	// Get source (optional for local plugins)
	var source string
	if attr, ok := pluginContent.Attributes["source"]; ok {
		sourceVal, diags := attr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("plugin '%s': %w", pluginName, diags)
		}
		source = sourceVal.AsString()
	}

	// Get version
	versionVal, diags := pluginContent.Attributes["version"].Expr.Value(ctx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("plugin '%s': %w", pluginName, diags)
	}

	p := &Plugin{
		Name:     pluginName,
		Source:   source,
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

// extractExpressionSource reads the raw source text of an HCL expression from its source file.
// For template strings like "Get weather for ${item.name}", this returns the text with
// interpolation placeholders intact, making it suitable for display in the UI.
func extractExpressionSource(expr hcl.Expression) string {
	rng := expr.Range()
	if rng.Filename == "" {
		return ""
	}
	src, err := os.ReadFile(rng.Filename)
	if err != nil {
		return ""
	}
	if rng.Start.Byte >= len(src) || rng.End.Byte > len(src) {
		return ""
	}
	raw := string(src[rng.Start.Byte:rng.End.Byte])
	// Strip surrounding quotes from HCL string literals
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	// Strip HCL heredoc markers (<<EOT / <<-EOT ... EOT)
	if strings.HasPrefix(raw, "<<") {
		// Find end of first line (the marker line)
		if idx := strings.Index(raw, "\n"); idx >= 0 {
			raw = raw[idx+1:]
		}
		// Find and remove the closing marker (last non-empty line)
		if idx := strings.LastIndex(raw, "\n"); idx >= 0 {
			raw = raw[:idx]
		}
		raw = strings.TrimSpace(raw)
	}
	return raw
}
