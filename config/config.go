package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"

	schemafunc "squadron/config/functions"
	vaultpkg "squadron/config/vault"
	squadronmcp "squadron/mcp"
	"squadron/mcp/oauth"
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
	Gateway     *Gateway     `hcl:"-"` // Parsed manually — at most one per config; settings come from a child block
	MCPServers  []MCPServer  `hcl:"-"`
	Missions   []Mission   `hcl:"mission,block"`
	Skills     []Skill     `hcl:"-"`

	// Storage configuration (optional, defaults to memory backend)
	Storage *StorageConfig `hcl:"-"`

	// CommandCenter configuration (optional, nil when absent = standalone mode)
	CommandCenter *CommandCenterConfig `hcl:"-"`

	// MCPHost configures Squadron acting AS an MCP server (was `mcp { ... }`,
	// renamed to `mcp_host { ... }`). nil when the block is absent.
	MCPHost *MCPHostConfig `hcl:"-"`

	// File browser configurations (optional)
	SharedFolders []SharedFolder `hcl:"-"`

	// LoadedPlugins holds the loaded plugin clients, keyed by plugin name
	LoadedPlugins map[string]*plugin.PluginClient `hcl:"-"`
	// LoadedMCPClients holds the loaded consumer-side MCP clients, keyed by
	// the HCL `mcp "name"` label. Entries may be nil when a server failed
	// to load — check LoadedMCPErrors for the reason. A name that is absent
	// from both maps did not appear in the config at all.
	LoadedMCPClients map[string]*squadronmcp.Client `hcl:"-"`
	// LoadedMCPErrors holds per-server load failures, keyed by the same
	// mcp "name" label. Populated for auth-required and transport errors
	// that the config loader tolerates rather than failing the whole
	// parse — see the Stage 1.5b load path in loadFromFiles for the exact
	// tolerance rules. Consumers (squadron mcp status, chat/mission/serve
	// tool resolution) use this to distinguish "MCP is anonymous and
	// working" from "MCP is broken" without re-probing the server.
	LoadedMCPErrors map[string]error `hcl:"-"`
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

// LoadMCPHost extracts only the mcp_host block from the config files at the
// given path without evaluating any expressions or loading consumer MCP
// servers. This is used by the serve command so the MCP host can start
// before the full config load runs (which may include consumer mcp "name"
// blocks that target the host's own URL — without this two-phase approach
// the consumer would try to dial localhost:<port>/mcp before the host is
// listening, deadlocking on itself).
//
// Returns nil (with no error) if the config files can't be found, can't be
// parsed, or simply don't declare an mcp_host block. The caller is expected
// to follow this up with a full Load — any real config errors will surface
// there with a complete diagnostic.
func LoadMCPHost(path string) *MCPHostConfig {
	files, err := resolveConfigFiles(path)
	if err != nil {
		return nil
	}

	parser := hclparse.NewParser()
	for _, file := range files {
		hclFile, diags := parser.ParseHCLFile(file)
		if diags.HasErrors() {
			continue
		}
		content, _, diags := hclFile.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{
				{Type: "mcp_host"},
			},
		})
		if diags.HasErrors() {
			continue
		}
		for _, block := range content.Blocks {
			if block.Type != "mcp_host" {
				continue
			}
			var mc MCPHostConfig
			if diags := gohcl.DecodeBody(block.Body, nil, &mc); diags.HasErrors() {
				continue
			}
			mc.Defaults()
			return &mc
		}
	}
	return nil
}

// LoadMCPSpecs does a lightweight HCL walk of the config and returns every
// mcp "name" { ... } block it finds, with variable references resolved. It
// deliberately does NOT load plugins, dial MCP servers, resolve agents, or
// do any other Stage 1.5+ work — the whole point is to give CLI commands
// that only care about MCP block contents (squadron mcp status, squadron
// mcp login, etc.) a fast, network-free way to read the config.
//
// Variable references inside mcp blocks are resolved against a minimal
// vars context built from any variable "..." blocks declared in the
// config, backed by the vault for values. Anything else (plugins, models,
// agents, tools, missions) is ignored even if it's malformed — a user
// whose entire config is broken can still run `squadron mcp status` to see
// which MCP blocks they declared and the auth state of their tokens.
//
// Returns nil specs (not an error) when the config path is empty or
// contains no mcp blocks. A non-nil error means HCL parsing itself failed
// in a way we could not recover from.
func LoadMCPSpecs(path string) ([]MCPServer, error) {
	files, err := resolveConfigFiles(path)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	// buildVarsContext reads the package-global configFuncs. Save and
	// restore it around our own load so we don't leave a stale load()
	// bound to this call's directory for a later call from a different
	// CWD (typical for CLI commands run across multiple projects).
	prevFuncs := configFuncs
	defer func() { configFuncs = prevFuncs }()
	configDir := filepath.Dir(files[0])
	configFuncs = schemafunc.SchemaFunctions()
	configFuncs["load"] = schemafunc.MakeLoadFunc(configDir)

	parser := hclparse.NewParser()

	// First pass: collect variable and mcp blocks from every file. We
	// tolerate parse errors inside individual files so one broken file
	// cannot block the whole command — downstream callers will surface
	// them via LoadAndValidate.
	type fileBlocks struct {
		variables []*hcl.Block
		mcp       []*hcl.Block
	}
	perFile := make([]fileBlocks, 0, len(files))
	for _, file := range files {
		hclFile, diags := parser.ParseHCLFile(file)
		if diags.HasErrors() {
			continue
		}
		content, _, diags := hclFile.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{
				{Type: "variable", LabelNames: []string{"name"}},
				{Type: "mcp", LabelNames: []string{"name"}},
			},
		})
		if diags.HasErrors() {
			continue
		}
		var fb fileBlocks
		for _, block := range content.Blocks {
			switch block.Type {
			case "variable":
				fb.variables = append(fb.variables, block)
			case "mcp":
				fb.mcp = append(fb.mcp, block)
			}
		}
		perFile = append(perFile, fb)
	}

	// Decode all variable blocks. gohcl handles the schema for Variable
	// directly, same as Stage 1 of the full loader.
	var allVars []Variable
	for _, pf := range perFile {
		for _, block := range pf.variables {
			var v Variable
			v.Name = block.Labels[0]
			if diags := gohcl.DecodeBody(block.Body, nil, &v); diags.HasErrors() {
				continue
			}
			allVars = append(allVars, v)
		}
	}

	varsCtx, _ := buildVarsContext(allVars)

	// Decode all mcp blocks against the vars context. We reuse the same
	// parseMCPServerBlock helper Stage 1.5b uses in the full loader so
	// any future attribute additions flow through one place.
	var servers []MCPServer
	for _, pf := range perFile {
		for _, block := range pf.mcp {
			srv, err := parseMCPServerBlock(block, varsCtx)
			if err != nil {
				continue // skip malformed blocks rather than fail the whole call
			}
			if err := srv.Validate(); err != nil {
				continue
			}
			servers = append(servers, *srv)
		}
	}
	return servers, nil
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
				{Type: "mcp_host"},
				{Type: "mcp", LabelNames: []string{"name"}},
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
			case "mcp_host":
				if partial.MCPHost == nil {
					var mc MCPHostConfig
					if diags := gohcl.DecodeBody(block.Body, nil, &mc); !diags.HasErrors() {
						mc.Defaults()
						partial.MCPHost = &mc
					}
				}
			case "mcp":
				// Best-effort: record the name only. Consumer-side MCP blocks
				// support HCL expressions in several fields that we can't
				// evaluate in the nil-context partial loader — surfacing the
				// name is enough for the error-display use case.
				if len(block.Labels) > 0 {
					partial.MCPServers = append(partial.MCPServers, MCPServer{Name: block.Labels[0]})
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

	if c.MCPHost != nil {
		if err := c.MCPHost.Validate(); err != nil {
			return fmt.Errorf("mcp_host: %w", err)
		}
	}

	// Validate consumer-side MCP server blocks and check for name collisions
	// with plugins.
	mcpNames := make(map[string]bool, len(c.MCPServers))
	for i := range c.MCPServers {
		s := &c.MCPServers[i]
		if err := s.Validate(); err != nil {
			return err
		}
		if mcpNames[s.Name] {
			return fmt.Errorf("duplicate mcp server name '%s'", s.Name)
		}
		mcpNames[s.Name] = true
	}
	for _, p := range c.Plugins {
		if mcpNames[p.Name] {
			return fmt.Errorf("name collision: plugin '%s' and mcp '%s' share the same name", p.Name, p.Name)
		}
	}

	for i := range c.Agents {
		if err := c.Agents[i].Validate(); err != nil {
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

	// Add consumer-side MCP server tools. Clients may be nil when an
	// HTTP MCP failed to load because it needs OAuth and hasn't been
	// authorized yet — still register the ".all" reference so static
	// HCL validation passes; resolving individual tool refs fails at
	// agent wire-up, and the user gets a pointed error at runtime.
	for mcpName, client := range c.LoadedMCPClients {
		if client != nil {
			tools, err := client.ListTools()
			if err == nil {
				for _, t := range tools {
					validToolRefs[fmt.Sprintf("mcp.%s.%s", mcpName, t.Name)] = true
				}
			}
		}
		validToolRefs[fmt.Sprintf("mcp.%s.all", mcpName)] = true
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
	Vault     []*hcl.Block
	Variables []*hcl.Block
	Models    []*hcl.Block
	Agents    []*hcl.Block
	Tools     []*hcl.Block
	Plugins   []*hcl.Block
	MCPServers []*hcl.Block
	Missions  []*hcl.Block
	Storage       []*hcl.Block
	CommandCenter []*hcl.Block
	SharedFolders []*hcl.Block
	MCPHost       []*hcl.Block
	Skills        []*hcl.Block
	Gateways      []*hcl.Block
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
				{Type: "vault"},
				{Type: "variable", LabelNames: []string{"name"}},
				{Type: "model", LabelNames: []string{"name"}},
				{Type: "agent", LabelNames: []string{"name"}},
				{Type: "tool", LabelNames: []string{"name"}},
				{Type: "plugin", LabelNames: []string{"name"}},
				{Type: "mission", LabelNames: []string{"name"}},
				{Type: "storage"},
				{Type: "command_center"},
				{Type: "shared_folder", LabelNames: []string{"name"}},
				{Type: "mcp_host"},
				{Type: "mcp", LabelNames: []string{"name"}},
				{Type: "skill", LabelNames: []string{"name"}},
				{Type: "gateway", LabelNames: []string{"name"}},
			},
		})
		if diags.HasErrors() {
			return nil, fmt.Errorf("[2] partial content %s: %w", file, diags)
		}

		var pb parsedBlocks
		for _, block := range content.Blocks {
			switch block.Type {
			case "vault":
				pb.Vault = append(pb.Vault, block)
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
			case "mcp_host":
				pb.MCPHost = append(pb.MCPHost, block)
			case "mcp":
				pb.MCPServers = append(pb.MCPServers, block)
			case "skill":
				pb.Skills = append(pb.Skills, block)
			case "gateway":
				pb.Gateways = append(pb.Gateways, block)
			}
		}
		allParsedBlocks = append(allParsedBlocks, pb)
	}

	// Stage 0: vault block. Decoded with a nil context because it
	// cannot reference vars — the block is what decides how to
	// decrypt vars.vault in the first place.
	var vaultConfig *VaultConfig
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Vault {
			if vaultConfig != nil {
				return nil, fmt.Errorf("vault block declared more than once")
			}
			var vc VaultConfig
			if diags := gohcl.DecodeBody(block.Body, nil, &vc); diags.HasErrors() {
				return nil, fmt.Errorf("vault block: %w", diags)
			}
			vaultConfig = &vc
		}
	}
	providerName := ""
	if vaultConfig != nil {
		providerName = vaultConfig.Provider
	}
	if err := vaultpkg.SetActiveProviderName(providerName); err != nil {
		return nil, fmt.Errorf("vault block: %w", err)
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

	// Parse storage block (optional — defaults to sqlite if omitted)
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
		configDir := "."
		if len(files) > 0 {
			configDir = filepath.Dir(files[0])
		}
		storageConfig = *DefaultStorageConfig(configDir)
	}
	storageConfig.Defaults()

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
				{Name: "aliases"},
				{Name: "api_key"},
				{Name: "base_url"},
				{Name: "prompt_caching"},
				{Name: "reasoning_models"},
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

		if attr, ok := content.Attributes["reasoning_models"]; ok {
			val, d := attr.Expr.Value(ctx)
			if d.HasErrors() {
				return nil, d
			}
			for it := val.ElementIterator(); it.Next(); {
				_, v := it.Element()
				m.ReasoningModels = append(m.ReasoningModels, v.AsString())
			}
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

	// Parse mcp_host block (optional singleton, with vars context). This used
	// to be `mcp { ... }` but was renamed to free up the `mcp` keyword for
	// labeled consumer-side blocks.
	var mcpHostConfig *MCPHostConfig
	for _, pb := range allParsedBlocks {
		for _, block := range pb.MCPHost {
			var mc MCPHostConfig
			diags := gohcl.DecodeBody(block.Body, varsCtx, &mc)
			if diags.HasErrors() {
				return nil, fmt.Errorf("mcp_host: %w", diags)
			}
			mc.Defaults()
			mcpHostConfig = &mc
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
				if err := configureWithRetry(client, p.Name, p.Settings); err != nil {
					return nil, err
				}
			}
		}
	}

	// Stage 1.5a: Parse the optional gateway block. Squadron supports
	// at most one gateway per instance — extra blocks are a config
	// error caught here. Subprocess lifecycle (download, launch,
	// configure) is owned by `cmd/engage.go` so `verify` and direct
	// `mission` runs don't pay the startup cost.
	var gatewayCfg *Gateway
	for _, pb := range allParsedBlocks {
		for _, block := range pb.Gateways {
			g, err := parseGatewayBlock(block, varsCtx)
			if err != nil {
				return nil, err
			}
			if err := g.Validate(); err != nil {
				return nil, err
			}
			if gatewayCfg != nil {
				return nil, fmt.Errorf("gateway %q: only one gateway block is supported per config (found %q earlier)", g.Name, gatewayCfg.Name)
			}
			gatewayCfg = g
		}
	}

	// Stage 1.5b: Load consumer-side MCP servers (same vars context as plugins).
	// For source-backed servers this triggers the auto-installer on first load
	// (npm install or github release download); cached installs skip the network.
	//
	// Per-server load failures are tolerated: the error is recorded in
	// loadedMCPErrors and a nil entry is stored in loadedMCPClients. An agent
	// that references an unloaded MCP's tools sees an empty tool set and
	// surfaces the failure when it tries to call one. This lets the rest of
	// squadron (verify, status, login, missions that don't use the broken
	// MCP) keep working while the user fixes or logs into the bad server.
	var allMCPServers []MCPServer
	loadedMCPClients := make(map[string]*squadronmcp.Client)
	loadedMCPErrors := make(map[string]error)
	for _, pb := range allParsedBlocks {
		for _, block := range pb.MCPServers {
			srv, err := parseMCPServerBlock(block, varsCtx)
			if err != nil {
				return nil, err
			}
			if err := srv.Validate(); err != nil {
				return nil, err
			}
			allMCPServers = append(allMCPServers, *srv)

			// Persist config-level OAuth client credentials to the vault so
			// the login flow and transport pick them up automatically.
			if srv.ClientID != "" {
				if err := oauth.SaveClientCredentials(srv.Name, oauth.ClientCredentials{
					ClientID:     srv.ClientID,
					ClientSecret: srv.ClientSecret,
				}); err != nil {
					return nil, fmt.Errorf("mcp '%s': saving client credentials: %w", srv.Name, err)
				}
			}

			client, err := squadronmcp.Load(srv.Name, squadronmcp.Spec{
				Command: srv.Command,
				URL:     srv.URL,
				Headers: srv.Headers,
				Source:  srv.Source,
				Version: srv.Version,
				Entry:   srv.Entry,
				Args:    srv.Args,
				Env:     srv.Env,
			})
			if err != nil {
				loadedMCPClients[srv.Name] = nil
				loadedMCPErrors[srv.Name] = err
				continue
			}
			loadedMCPClients[srv.Name] = client
		}
	}

	// Build plugins + mcp context for HCL evaluation
	pluginsCtx := buildPluginsContext(varsCtx, loadedPlugins, loadedMCPClients)

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
		Variables:        allVars,
		Models:           allModels,
		Agents:           allAgents,
		CustomTools:      allTools,
		Plugins:          allPlugins,
		MCPServers:       allMCPServers,
		Missions:         allMissions,
		Skills:           allSkills,
		Storage:          &storageConfig,
		CommandCenter:    commandCenterConfig,
		MCPHost:          mcpHostConfig,
		SharedFolders:    allSharedFolders,
		LoadedPlugins:    loadedPlugins,
		LoadedMCPClients: loadedMCPClients,
		LoadedMCPErrors:  loadedMCPErrors,
		ResolvedVars:     resolvedVars,
		Gateway:          gatewayCfg,
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
// configureWithRetry retries plugin Configure() a few times to handle the case
// where the gRPC process isn't fully ready immediately after launch.
func configureWithRetry(client *plugin.PluginClient, name string, settings map[string]string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = client.Configure(settings); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("plugin '%s' failed to configure: %w", name, err)
}

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

	// Expose every vault key as vars.<name> with no declaration required.
	for k, val := range fileVars {
		varsMap[k] = cty.StringVal(val)
	}

	// Declared variables still apply defaults / empty fallback when the vault
	// has no value for them. A vault value always wins over a declared default.
	for _, v := range vars {
		if _, ok := varsMap[v.Name]; ok {
			continue
		}
		if v.Default != "" {
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

// buildPluginsContext adds builtins, plugins, and mcp namespaces to the
// existing eval context. Each namespace exposes <ns>.<name>.<tool> and
// <ns>.<name>.all references that HCL expressions can use in `tools = [...]`.
func buildPluginsContext(ctx *hcl.EvalContext, loadedPlugins map[string]*plugin.PluginClient, loadedMCPClients map[string]*squadronmcp.Client) *hcl.EvalContext {
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

	// Build mcp namespace (consumer-side MCP servers). A nil client means
	// this MCP failed to load (e.g. OAuth not yet authorized); we still
	// register a namespace entry so references like `mcp.<name>.all` in
	// agent blocks parse cleanly — they'll resolve to empty tool sets at
	// wire-up time and surface the underlying error there.
	mcpMap := make(map[string]cty.Value)
	for mcpName, client := range loadedMCPClients {
		toolsMap := make(map[string]cty.Value)
		if client != nil {
			tools, err := client.ListTools()
			if err == nil {
				for _, t := range tools {
					toolsMap[t.Name] = cty.StringVal(fmt.Sprintf("mcp.%s.%s", mcpName, t.Name))
				}
			}
		}
		toolsMap["all"] = cty.StringVal(fmt.Sprintf("mcp.%s.all", mcpName))
		mcpMap[mcpName] = cty.ObjectVal(toolsMap)
	}

	// Copy existing vars and add the three namespaces
	newVars := make(map[string]cty.Value)
	for k, v := range ctx.Variables {
		newVars[k] = v
	}
	newVars["builtins"] = cty.ObjectVal(builtinsMap)
	if len(pluginsMap) > 0 {
		newVars["plugins"] = cty.ObjectVal(pluginsMap)
	}
	if len(mcpMap) > 0 {
		newVars["mcp"] = cty.ObjectVal(mcpMap)
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
			{Name: "reasoning"},
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
	if attr, ok := content.Attributes["reasoning"]; ok {
		val, d := attr.Expr.Value(agentCtx)
		if d.HasErrors() {
			return nil, fmt.Errorf("agent '%s' reasoning: %w", a.Name, d)
		}
		a.Reasoning = val.AsString()
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
			{Type: "run_folder"},
			{Type: "schedule"},
			{Type: "trigger"},
			{Type: "budget"},
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
				{Name: "reasoning"},
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

		// Optional reasoning attribute
		if reasoningAttr, ok := cmdContent.Attributes["reasoning"]; ok {
			reasoningVal, reasoningDiags := reasoningAttr.Expr.Value(ctx)
			if reasoningDiags.HasErrors() {
				return nil, fmt.Errorf("mission '%s' commander reasoning: %w", missionName, reasoningDiags)
			}
			missionCommander.Reasoning = reasoningVal.AsString()
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

	// Parse optional folder block (dedicated mission folder, reserved name "mission")
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

	// Parse optional run_folder block (per-run ephemeral folder, reserved name "run")
	var missionRunFolder *MissionRunFolder
	for _, rfBlock := range missionContent.Blocks {
		if rfBlock.Type != "run_folder" {
			continue
		}
		if missionRunFolder != nil {
			return nil, fmt.Errorf("mission '%s': only one run_folder block allowed", missionName)
		}
		var rf MissionRunFolder
		diags := gohcl.DecodeBody(rfBlock.Body, ctx, &rf)
		if diags.HasErrors() {
			return nil, fmt.Errorf("mission '%s' run_folder: %w", missionName, diags)
		}
		if rf.Cleanup == nil {
			v := DefaultRunFolderCleanupDays
			rf.Cleanup = &v
		}
		missionRunFolder = &rf
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

	// Parse budget block (optional, singleton)
	var missionBudget *Budget
	for _, budgetBlock := range missionContent.Blocks {
		if budgetBlock.Type != "budget" {
			continue
		}
		if missionBudget != nil {
			return nil, fmt.Errorf("mission '%s': only one budget block allowed", missionName)
		}
		b, err := parseBudgetBlock(budgetBlock, ctx)
		if err != nil {
			return nil, fmt.Errorf("mission '%s' budget: %w", missionName, err)
		}
		missionBudget = b
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
		RunFolder:   missionRunFolder,
		Schedules:   schedules,
		Trigger:     trigger,
		MaxParallel: maxParallel,
		Budget:      missionBudget,
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
			{Type: "budget"},
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

	// Parse budget block if present
	var taskBudget *Budget
	for _, budgetBlock := range taskContent.Blocks {
		if budgetBlock.Type != "budget" {
			continue
		}
		if taskBudget != nil {
			return nil, fmt.Errorf("task '%s': only one budget block allowed", taskName)
		}
		b, err := parseBudgetBlock(budgetBlock, ctx)
		if err != nil {
			return nil, fmt.Errorf("task '%s' budget: %w", taskName, err)
		}
		taskBudget = b
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
		Budget:        taskBudget,
	}, nil
}

// parseBudgetBlock parses a `budget { tokens = N, dollars = M }` block.
// Both attributes are optional but at least one must be set (enforced by Validate).
func parseBudgetBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Budget, error) {
	content, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "tokens"},
			{Name: "dollars"},
		},
	})
	if diags.HasErrors() {
		return nil, diags
	}

	b := &Budget{}
	if attr, ok := content.Attributes["tokens"]; ok {
		val, diags := attr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("tokens: %w", diags)
		}
		bf := val.AsBigFloat()
		if !bf.IsInt() {
			return nil, fmt.Errorf("tokens must be an integer")
		}
		n, _ := bf.Int64()
		b.Tokens = &n
	}
	if attr, ok := content.Attributes["dollars"]; ok {
		val, diags := attr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("dollars: %w", diags)
		}
		f, _ := val.AsBigFloat().Float64()
		b.Dollars = &f
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return b, nil
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

// parseGatewayBlock parses a `gateway "name" { source, version, settings { ... } }`
// block. Mirrors the plugin parser since gateways follow the same source +
// version + settings shape on disk.
func parseGatewayBlock(block *hcl.Block, ctx *hcl.EvalContext) (*Gateway, error) {
	name := block.Labels[0]

	content, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "source"},
			{Name: "version", Required: true},
			{Name: "settings"},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "settings"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("gateway %q: %w", name, diags)
	}

	g := &Gateway{Name: name, Settings: make(map[string]string)}

	if attr, ok := content.Attributes["source"]; ok {
		val, diags := attr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("gateway %q: %w", name, diags)
		}
		g.Source = val.AsString()
	}

	versionVal, diags := content.Attributes["version"].Expr.Value(ctx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("gateway %q: %w", name, diags)
	}
	g.Version = versionVal.AsString()

	storeSetting := func(k string, v cty.Value) error {
		switch {
		case v.Type() == cty.String:
			g.Settings[k] = v.AsString()
		case v.Type() == cty.Bool:
			g.Settings[k] = fmt.Sprintf("%v", v.True())
		case v.Type() == cty.Number:
			g.Settings[k] = v.AsBigFloat().String()
		default:
			return fmt.Errorf("gateway %q setting %q: must be a string/bool/number primitive", name, k)
		}
		return nil
	}

	// Attribute-style: `settings = { key = value, ... }` (documented form).
	if attr, ok := content.Attributes["settings"]; ok {
		val, diags := attr.Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("gateway %q settings: %w", name, diags)
		}
		if !val.Type().IsObjectType() && !val.Type().IsMapType() {
			return nil, fmt.Errorf("gateway %q: settings must be an object", name)
		}
		it := val.ElementIterator()
		for it.Next() {
			k, v := it.Element()
			if err := storeSetting(k.AsString(), v); err != nil {
				return nil, err
			}
		}
	}

	// Block-style: `settings { key = value }` (legacy form, kept for parity
	// with how plugin settings are written).
	for _, settingsBlock := range content.Blocks {
		if settingsBlock.Type != "settings" {
			continue
		}
		attrs, diags := settingsBlock.Body.JustAttributes()
		if diags.HasErrors() {
			return nil, fmt.Errorf("gateway %q settings: %w", name, diags)
		}
		for k, attr := range attrs {
			val, diags := attr.Expr.Value(ctx)
			if diags.HasErrors() {
				return nil, fmt.Errorf("gateway %q setting %q: %w", name, k, diags)
			}
			if err := storeSetting(k, val); err != nil {
				return nil, err
			}
		}
	}

	return g, nil
}

// parseMCPServerBlock parses a consumer-side `mcp "name" { ... }` block.
// All fields are optional at parse time — Validate() on the returned struct
// enforces the exactly-one-of command/url/source rule and the transport-gated
// field rules.
func parseMCPServerBlock(block *hcl.Block, ctx *hcl.EvalContext) (*MCPServer, error) {
	name := block.Labels[0]

	content, _, diags := block.Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "command"},
			{Name: "url"},
			{Name: "source"},
			{Name: "version"},
			{Name: "entry"},
			{Name: "args"},
			{Name: "env"},
			{Name: "headers"},
			{Name: "client_id"},
			{Name: "client_secret"},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("mcp '%s': %w", name, diags)
	}

	srv := &MCPServer{Name: name}

	getString := func(key string) (string, error) {
		attr, ok := content.Attributes[key]
		if !ok {
			return "", nil
		}
		val, d := attr.Expr.Value(ctx)
		if d.HasErrors() {
			return "", fmt.Errorf("mcp '%s': %s: %w", name, key, d)
		}
		if val.Type() != cty.String {
			return "", fmt.Errorf("mcp '%s': %s must be a string", name, key)
		}
		return val.AsString(), nil
	}

	getStringList := func(key string) ([]string, error) {
		attr, ok := content.Attributes[key]
		if !ok {
			return nil, nil
		}
		val, d := attr.Expr.Value(ctx)
		if d.HasErrors() {
			return nil, fmt.Errorf("mcp '%s': %s: %w", name, key, d)
		}
		var out []string
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			if v.Type() != cty.String {
				return nil, fmt.Errorf("mcp '%s': %s entries must be strings", name, key)
			}
			out = append(out, v.AsString())
		}
		return out, nil
	}

	getStringMap := func(key string) (map[string]string, error) {
		attr, ok := content.Attributes[key]
		if !ok {
			return nil, nil
		}
		val, d := attr.Expr.Value(ctx)
		if d.HasErrors() {
			return nil, fmt.Errorf("mcp '%s': %s: %w", name, key, d)
		}
		out := make(map[string]string)
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if k.Type() != cty.String {
				return nil, fmt.Errorf("mcp '%s': %s keys must be strings", name, key)
			}
			switch {
			case v.Type() == cty.String:
				out[k.AsString()] = v.AsString()
			case v.Type() == cty.Bool:
				out[k.AsString()] = fmt.Sprintf("%v", v.True())
			case v.Type() == cty.Number:
				out[k.AsString()] = v.AsBigFloat().String()
			default:
				return nil, fmt.Errorf("mcp '%s': %s values must be primitives", name, key)
			}
		}
		return out, nil
	}

	var err error
	if srv.Command, err = getString("command"); err != nil {
		return nil, err
	}
	if srv.URL, err = getString("url"); err != nil {
		return nil, err
	}
	if srv.Source, err = getString("source"); err != nil {
		return nil, err
	}
	if srv.Version, err = getString("version"); err != nil {
		return nil, err
	}
	if srv.Entry, err = getString("entry"); err != nil {
		return nil, err
	}
	if srv.Args, err = getStringList("args"); err != nil {
		return nil, err
	}
	if srv.Env, err = getStringMap("env"); err != nil {
		return nil, err
	}
	if srv.Headers, err = getStringMap("headers"); err != nil {
		return nil, err
	}
	if srv.ClientID, err = getString("client_id"); err != nil {
		return nil, err
	}
	if srv.ClientSecret, err = getString("client_secret"); err != nil {
		return nil, err
	}

	return srv, nil
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
