package wsbridge

import (
	"squadron/aitools"
	"squadron/config"
	"squadron/plugin"

	"github.com/mlund01/squadron-sdk/protocol"
)

// ConfigToInstanceConfig converts squadron's HCL-based config into a JSON-safe protocol.InstanceConfig.
func ConfigToInstanceConfig(cfg *config.Config) protocol.InstanceConfig {
	ic := protocol.InstanceConfig{}

	for _, m := range cfg.Models {
		model := ""
		if len(m.AllowedModels) > 0 {
			model = m.AllowedModels[0]
		}
		ic.Models = append(ic.Models, protocol.ModelInfo{
			Name:     m.Name,
			Provider: string(m.Provider),
			Model:    model,
		})
	}

	for _, a := range cfg.Agents {
		ic.Agents = append(ic.Agents, protocol.AgentInfo{
			Name:  a.Name,
			Role:  a.Role,
			Model: a.Model,
			Tools: a.Tools,
		})
	}

	for _, m := range cfg.Missions {
		mi := protocol.MissionInfo{
			Name:        m.Name,
			Description: m.Directive,
			Commander:   m.Commander,
			Agents:      m.Agents,
		}
		for _, ds := range m.Datasets {
			di := protocol.DatasetInfo{
				Name:        ds.Name,
				Description: ds.Description,
				BindTo:      ds.BindTo,
			}
			if ds.Schema != nil {
				for _, f := range ds.Schema.Fields {
					di.Schema = append(di.Schema, protocol.DatasetField{
						Name:     f.Name,
						Type:     f.Type,
						Required: f.Required,
					})
				}
			}
			mi.Datasets = append(mi.Datasets, di)
		}
		for _, inp := range m.Inputs {
			mi.Inputs = append(mi.Inputs, protocol.MissionInputInfo{
				Name:        inp.Name,
				Description: inp.Description,
				Type:        inp.Type,
				Required:    inp.Default == nil && !inp.Secret,
			})
		}
		for _, t := range m.Tasks {
			ti := protocol.TaskInfo{
				Name:      t.Name,
				Objective: t.RawObjective,
				DependsOn: t.DependsOn,
			}
			if len(t.Agents) > 0 {
				ti.Agent = t.Agents[0]
			}
			if t.Iterator != nil {
				ti.Iterator = &protocol.TaskIteratorInfo{
					Dataset:          t.Iterator.Dataset,
					Parallel:         t.Iterator.Parallel,
					MaxRetries:       t.Iterator.MaxRetries,
					ConcurrencyLimit: t.Iterator.ConcurrencyLimit,
				}
			}
			mi.Tasks = append(mi.Tasks, ti)
		}
		ic.Missions = append(ic.Missions, mi)
	}

	// Add builtin plugins (dataset is internal-only, not user-facing)
	for namespace, tools := range config.InternalPluginTools {
		if namespace == "dataset" {
			continue
		}
		pi := protocol.PluginInfo{
			Name:    namespace,
			Path:    "builtin",
			Builtin: true,
		}
		for _, toolName := range tools {
			ref := "plugins." + namespace + "." + toolName
			if tool := config.GetInternalPluginTool(ref, nil); tool != nil {
				ti := aitoolToProtocolToolInfo(tool)
				ti.Name = toolName // Use config-level name, not legacy ToolName()
				pi.Tools = append(pi.Tools, ti)
			}
		}
		ic.Plugins = append(ic.Plugins, pi)
	}

	// Add external plugins
	for _, p := range cfg.Plugins {
		pi := protocol.PluginInfo{
			Name: p.Name,
			Path: p.Source,
		}
		if client, ok := cfg.LoadedPlugins[p.Name]; ok {
			if tools, err := client.ListTools(); err == nil {
				for _, t := range tools {
					pi.Tools = append(pi.Tools, pluginToolInfoToProtocol(t))
				}
			}
		}
		ic.Plugins = append(ic.Plugins, pi)
	}

	for _, v := range cfg.Variables {
		ic.Variables = append(ic.Variables, protocol.VariableInfo{
			Name:   v.Name,
			Secret: v.Secret,
		})
	}

	return ic
}

// aitoolToProtocolToolInfo converts an aitools.Tool to protocol.ToolInfo.
func aitoolToProtocolToolInfo(t aitools.Tool) protocol.ToolInfo {
	schema := t.ToolPayloadSchema()
	return protocol.ToolInfo{
		Name:        t.ToolName(),
		Description: t.ToolDescription(),
		Parameters:  convertAIToolSchema(schema),
	}
}

// pluginToolInfoToProtocol converts a plugin.ToolInfo to protocol.ToolInfo.
func pluginToolInfoToProtocol(t *plugin.ToolInfo) protocol.ToolInfo {
	return protocol.ToolInfo{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  convertAIToolSchema(t.Schema),
	}
}

func convertAIToolSchema(s aitools.Schema) *protocol.ToolSchema {
	if len(s.Properties) == 0 {
		return nil
	}
	ts := &protocol.ToolSchema{
		Type:       string(s.Type),
		Properties: make(map[string]protocol.ToolProperty, len(s.Properties)),
		Required:   s.Required,
	}
	for name, prop := range s.Properties {
		ts.Properties[name] = convertAIToolProperty(prop)
	}
	return ts
}

func convertAIToolProperty(p aitools.Property) protocol.ToolProperty {
	tp := protocol.ToolProperty{
		Type:        string(p.Type),
		Description: p.Description,
		Required:    p.Required,
	}
	if p.Items != nil {
		items := convertAIToolProperty(*p.Items)
		tp.Items = &items
	}
	if len(p.Properties) > 0 {
		tp.Properties = make(map[string]protocol.ToolProperty, len(p.Properties))
		for name, nested := range p.Properties {
			tp.Properties[name] = convertAIToolProperty(nested)
		}
	}
	return tp
}

