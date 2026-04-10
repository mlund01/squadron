package wsbridge

import (
	"strings"

	"squadron/aitools"
	"squadron/config"
	"squadron/plugin"

	"github.com/mlund01/squadron-wire/protocol"
)

// ConfigToInstanceConfig converts squadron's HCL-based config into a JSON-safe protocol.InstanceConfig.
func ConfigToInstanceConfig(cfg *config.Config) protocol.InstanceConfig {
	ic := protocol.InstanceConfig{}
	if cfg == nil {
		return ic
	}

	for _, m := range cfg.Models {
		// Pick first available model name for display
		model := ""
		for key := range m.AvailableModels() {
			model = key
			break
		}
		ic.Models = append(ic.Models, protocol.ModelInfo{
			Name:     m.Name,
			Provider: string(m.Provider),
			Model:    model,
		})
	}

	for _, a := range cfg.Agents {
		// Build skill names: explicit refs + local skill names
		var skillNames []string
		for _, ref := range a.Skills {
			skillNames = append(skillNames, strings.TrimPrefix(ref, "skills."))
		}
		for _, ls := range a.LocalSkills {
			skillNames = append(skillNames, ls.Name)
		}
		ic.Agents = append(ic.Agents, protocol.AgentInfo{
			Name:   a.Name,
			Role:   a.Role,
			Model:  a.Model,
			Tools:  a.Tools,
			Skills: skillNames,
		})
	}

	// Add global skills
	for _, s := range cfg.Skills {
		ic.Skills = append(ic.Skills, protocol.SkillInfo{
			Name:         s.Name,
			Description:  s.Description,
			Instructions: s.Instructions,
			Tools:        s.Tools,
		})
	}

	// Add agent-scoped skills
	for _, a := range cfg.Agents {
		for _, s := range a.LocalSkills {
			ic.Skills = append(ic.Skills, protocol.SkillInfo{
				Name:         s.Name,
				Description:  s.Description,
				Instructions: s.Instructions,
				Tools:        s.Tools,
				Agent:        a.Name,
			})
		}
	}

	// Add mission-scoped agents and their local skills
	for _, m := range cfg.Missions {
		for _, a := range m.LocalAgents {
			var mSkillNames []string
			for _, ref := range a.Skills {
				mSkillNames = append(mSkillNames, strings.TrimPrefix(ref, "skills."))
			}
			for _, ls := range a.LocalSkills {
				mSkillNames = append(mSkillNames, ls.Name)
			}
			ic.Agents = append(ic.Agents, protocol.AgentInfo{
				Name:    a.Name,
				Role:    a.Role,
				Model:   a.Model,
				Tools:   a.Tools,
				Skills:  mSkillNames,
				Mission: m.Name,
			})
			for _, s := range a.LocalSkills {
				ic.Skills = append(ic.Skills, protocol.SkillInfo{
					Name:         s.Name,
					Description:  s.Description,
					Instructions: s.Instructions,
					Tools:        s.Tools,
					Agent:        a.Name,
				})
			}
		}
	}

	for _, m := range cfg.Missions {
		mi := protocol.MissionInfo{
			Name:        m.Name,
			Description: m.Directive,
			Commander:   m.Commander.Model,
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
			mi.Inputs = append(mi.Inputs, convertMissionInput(inp))
		}
		for _, t := range m.Tasks {
			ti := protocol.TaskInfo{
				Name:      t.Name,
				Objective: t.RawObjective,
				DependsOn: t.DependsOn,
				SendTo:    t.SendTo,
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
			if t.Router != nil {
				ri := &protocol.TaskRouterInfo{}
				for _, route := range t.Router.Routes {
					ri.Routes = append(ri.Routes, protocol.TaskRouteInfo{
						Target:    route.Target,
						Condition: route.Condition,
						IsMission: route.IsMission,
					})
				}
				ti.Router = ri
			}
			mi.Tasks = append(mi.Tasks, ti)
		}
		// Map schedules
		for i := range m.Schedules {
			mi.Schedules = append(mi.Schedules, protocol.ScheduleInfo{
				Expression: m.Schedules[i].ToCron(),
				At:         m.Schedules[i].At,
				Every:      m.Schedules[i].Every,
				Weekdays:   m.Schedules[i].Weekdays,
				Timezone:   m.Schedules[i].Timezone,
				Inputs:     m.Schedules[i].Inputs,
			})
		}

		// Map trigger
		if m.Trigger != nil {
			mi.Trigger = &protocol.TriggerInfo{
				Type:        "webhook",
				WebhookPath: m.Trigger.WebhookPath,
				HasSecret:   m.Trigger.Secret != "",
				Secret:      m.Trigger.Secret,
			}
		}

		mi.MaxParallel = m.MaxParallel
		ic.Missions = append(ic.Missions, mi)
	}

	// Add builtin tools (dataset is internal-only, not user-facing)
	for namespace, tools := range config.BuiltinTools {
		if namespace == "dataset" {
			continue
		}
		pi := protocol.PluginInfo{
			Name:    namespace,
			Path:    "builtin",
			Builtin: true,
		}
		for _, toolName := range tools {
			ref := "builtins." + namespace + "." + toolName
			if tool := config.GetBuiltinTool(ref, nil); tool != nil {
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
			Name:    p.Name,
			Path:    p.Source,
			Version: p.Version,
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

	// Build shared folder → missions map
	sharedMissions := map[string][]string{}
	for _, m := range cfg.Missions {
		for _, folderName := range m.Folders {
			sharedMissions[folderName] = append(sharedMissions[folderName], m.Name)
		}
	}

	for _, fb := range cfg.SharedFolders {
		label := fb.Label
		if label == "" {
			label = fb.Name
		}
		ic.SharedFolders = append(ic.SharedFolders, protocol.SharedFolderInfo{
			Name:        fb.Name,
			Path:        fb.Path,
			Label:       label,
			Description: fb.Description,
			Editable:    fb.Editable,
			IsShared:    true,
			Missions:    sharedMissions[fb.Name],
		})
	}

	// Add dedicated mission folders
	for _, m := range cfg.Missions {
		if m.Folder != nil {
			ic.SharedFolders = append(ic.SharedFolders, protocol.SharedFolderInfo{
				Name:        m.Name,
				Path:        m.Folder.Path,
				Label:       m.Name,
				Description: m.Folder.Description,
				Editable:    true,
				IsShared:    false,
				Missions:    []string{m.Name},
			})
		}
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

func convertMissionInput(inp config.MissionInput) protocol.MissionInputInfo {
	info := protocol.MissionInputInfo{
		Name:        inp.Name,
		Description: inp.Description,
		Type:        inp.Type,
		Required:    inp.Default == nil && !inp.Protected,
		Protected:   inp.Protected,
	}
	if inp.Items != nil {
		items := convertMissionInput(*inp.Items)
		info.Items = &items
	}
	for _, prop := range inp.Properties {
		info.Properties = append(info.Properties, convertMissionInput(prop))
	}
	return info
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

