package mission

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"squadron/config"
	missionruntime "squadron/mission/runtime"
)

// CommanderControlOperation names a harness-owned operation exposed to the
// mission commander. These operations are deliberately not aitools.Tool
// implementations: mission and task tools must never be wired into the
// commander's model session.
type CommanderControlOperation string

const (
	ControlInspectMission CommanderControlOperation = "inspect_mission"
	ControlStartTask      CommanderControlOperation = "start_task"
	ControlInspectTask    CommanderControlOperation = "inspect_task"
	ControlSelectRoute    CommanderControlOperation = "select_route"
	ControlFinishMission  CommanderControlOperation = "finish_mission"
)

// CommanderControlSurface is the complete intrinsic operation set available
// to a mission commander. Returning a copy prevents callers from mutating
// the package-level policy.
func CommanderControlSurface() []CommanderControlOperation {
	return []CommanderControlOperation{
		ControlInspectMission,
		ControlStartTask,
		ControlInspectTask,
		ControlSelectRoute,
		ControlFinishMission,
	}
}

// ExecutionGrantContext contains run-owned values that are persisted with
// every resolved executor grant.
type ExecutionGrantContext struct {
	RunAs                string
	InputsJSON           string
	ConfigurationVersion string
}

// RuntimeGrantResolver binds declarative mission policy to one run identity
// without giving the commander any capability execution surface.
type RuntimeGrantResolver struct {
	policy  *CapabilityPolicy
	context ExecutionGrantContext
}

func NewRuntimeGrantResolver(m *config.Mission, context ExecutionGrantContext) (*RuntimeGrantResolver, error) {
	policy, err := NewCapabilityPolicy(m)
	if err != nil {
		return nil, err
	}
	if context.RunAs == "" {
		return nil, fmt.Errorf("run-as identity is required")
	}
	if context.ConfigurationVersion == "" {
		return nil, fmt.Errorf("configuration version is required")
	}
	return &RuntimeGrantResolver{policy: policy, context: context}, nil
}

func (r *RuntimeGrantResolver) ResolveExecutionGrant(request missionruntime.GrantRequest) (missionruntime.ExecutionGrant, error) {
	policyTaskName := request.TaskName
	if parent, _, ok := iterationParentAndIndex(request.TaskName); ok && r.policy.mission.GetTaskByName(request.TaskName) == nil {
		if task := r.policy.mission.GetTaskByName(parent); task != nil && task.Iterator != nil {
			policyTaskName = parent
		}
	}
	grant, err := r.policy.ResolveGrant(policyTaskName, request.Model, request.Tools)
	if err != nil {
		return missionruntime.ExecutionGrant{}, err
	}
	policyJSON, err := json.Marshal(grant)
	if err != nil {
		return missionruntime.ExecutionGrant{}, fmt.Errorf("encode execution policy: %w", err)
	}
	return missionruntime.ExecutionGrant{
		TaskName: request.TaskName, Model: grant.Model, Tools: append([]string(nil), grant.Tools...),
		RunAs: r.context.RunAs, InputsJSON: r.context.InputsJSON, PolicyJSON: string(policyJSON),
		ConfigurationVersion: r.context.ConfigurationVersion,
	}, nil
}

func iterationParentAndIndex(name string) (string, int, bool) {
	open := strings.LastIndexByte(name, '[')
	if open <= 0 || !strings.HasSuffix(name, "]") {
		return "", 0, false
	}
	index, err := strconv.Atoi(name[open+1 : len(name)-1])
	if err != nil || index < 0 || strconv.Itoa(index) != name[open+1:len(name)-1] {
		return "", 0, false
	}
	return name[:open], index, true
}

// RuntimeTaskDefinitions translates the declarative task graph and optional
// structured output contracts into the durable runtime's immutable form.
func RuntimeTaskDefinitions(m *config.Mission) ([]missionruntime.TaskDefinition, error) {
	return RuntimeTaskDefinitionsWithMissions(m, nil)
}

// RuntimeTaskDefinitionsWithMissions also resolves the public input contract
// for cross-mission routes. Protected destination inputs are intentionally
// excluded from commander-visible and harness-accepted route inputs.
func RuntimeTaskDefinitionsWithMissions(m *config.Mission, missions []config.Mission) ([]missionruntime.TaskDefinition, error) {
	if m == nil {
		return nil, fmt.Errorf("mission is required")
	}
	destinations := make(map[string]*config.Mission, len(missions))
	for index := range missions {
		destinations[missions[index].Name] = &missions[index]
	}
	definitions := make([]missionruntime.TaskDefinition, 0, len(m.Tasks))
	for _, task := range m.Tasks {
		definition := missionruntime.TaskDefinition{
			Name: task.Name, Objective: task.RawObjective,
			DependsOn:         append([]string(nil), task.DependsOn...),
			SendTo:            append([]string(nil), task.SendTo...),
			MaxRetries:        task.MaxRetries,
			RetryDelaySeconds: task.RetryDelaySeconds,
		}
		if task.Iterator != nil {
			definition.Iterator = &missionruntime.IterationPolicy{
				Dataset: task.Iterator.Dataset, Parallel: task.Iterator.Parallel,
				MaxRetries: task.Iterator.MaxRetries, ConcurrencyLimit: task.Iterator.ConcurrencyLimit,
				StartDelayMS: task.Iterator.StartDelay, Smoketest: task.Iterator.Smoketest,
			}
		}
		if task.Router != nil {
			for _, route := range task.Router.Routes {
				runtimeRoute := missionruntime.RouteDefinition{Target: route.Target, Condition: route.Condition, IsMission: route.IsMission}
				if route.IsMission {
					destination := destinations[route.Target]
					if destination == nil && len(missions) > 0 {
						return nil, fmt.Errorf("route target mission %q is not configured", route.Target)
					}
					if destination != nil {
						for _, input := range destination.Inputs {
							if input.Protected {
								continue
							}
							runtimeRoute.Inputs = append(runtimeRoute.Inputs, missionruntime.RouteInputDefinition{
								Name: input.Name, Type: input.Type, Description: input.Description, Required: input.Default == nil,
							})
						}
					}
				}
				definition.Routes = append(definition.Routes, runtimeRoute)
			}
		}
		if task.Output != nil {
			definition.ResultPolicy.Fields = runtimeResultFields(task.Output.Fields)
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func runtimeResultFields(fields []config.OutputField) []missionruntime.ResultField {
	result := make([]missionruntime.ResultField, len(fields))
	for index, field := range fields {
		result[index] = missionruntime.ResultField{
			Name: field.Name, Type: field.Type, Required: field.Required,
			Properties: runtimeResultFields(field.Properties),
		}
		if field.Items != nil {
			item := runtimeResultFields([]config.OutputField{*field.Items})[0]
			result[index].Items = &item
		}
	}
	return result
}

// TaskCapabilityGrant is the immutable capability snapshot attached to one
// task-executor dispatch. The snapshot is intended to be persisted alongside
// the dispatch for authorization, audit, resume, and replay.
type TaskCapabilityGrant struct {
	TaskName string   `json:"taskName"`
	Model    string   `json:"model"`
	Tools    []string `json:"tools,omitempty"`
}

// CapabilityPolicy resolves and validates task-executor grants. It has no
// method for executing tools; execution belongs to the executor runtime and
// credential broker after the harness accepts a dispatch.
type CapabilityPolicy struct {
	mission *config.Mission
}

func NewCapabilityPolicy(m *config.Mission) (*CapabilityPolicy, error) {
	if m == nil {
		return nil, fmt.Errorf("mission is required")
	}
	if !m.HasExecutorPolicy() {
		return nil, fmt.Errorf("mission '%s' does not declare task-executor capabilities", m.Name)
	}
	return &CapabilityPolicy{mission: m}, nil
}

// ResolveGrant validates a commander's requested model and optional
// mission-level tools for an executor. Task tools are always included. Task
// model declarations narrow the mission model pool; otherwise the full mission
// model pool is eligible.
func (p *CapabilityPolicy) ResolveGrant(taskName, model string, selectedMissionTools []string) (TaskCapabilityGrant, error) {
	task := p.mission.GetTaskByName(taskName)
	if task == nil {
		return TaskCapabilityGrant{}, fmt.Errorf("task '%s' is not declared by mission '%s'", taskName, p.mission.Name)
	}

	allowedModels := p.mission.Models
	if len(task.Models) > 0 {
		allowedModels = task.Models
	}
	if !containsString(allowedModels, model) {
		return TaskCapabilityGrant{}, fmt.Errorf("model '%s' is not allowed for task '%s'", model, taskName)
	}

	for _, toolRef := range selectedMissionTools {
		// Task tools are mandatory. A commander may redundantly name one in
		// its optional selection; deduplication below still grants it once.
		if containsString(task.Tools, toolRef) {
			continue
		}
		if !containsString(p.mission.Tools, toolRef) {
			return TaskCapabilityGrant{}, fmt.Errorf("tool '%s' is not in the mission capability pool", toolRef)
		}
	}

	tools := make([]string, 0, len(task.Tools)+len(selectedMissionTools))
	seen := make(map[string]struct{}, cap(tools))
	for _, toolRef := range append(append([]string{}, task.Tools...), selectedMissionTools...) {
		if _, ok := seen[toolRef]; ok {
			continue
		}
		seen[toolRef] = struct{}{}
		tools = append(tools, toolRef)
	}

	return TaskCapabilityGrant{
		TaskName: taskName,
		Model:    model,
		Tools:    tools,
	}, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
