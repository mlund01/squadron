package config

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// Input type constants
const (
	InputTypeString = "string"
	InputTypeNumber = "number"
	InputTypeBool   = "bool"
	InputTypeList   = "list"
	InputTypeObject = "object"
)

// WorkflowInput represents an input parameter for a workflow
type WorkflowInput struct {
	Name        string     // From HCL label
	Type        string     // "string", "number", "bool", "list", "object"
	Description string     // Documentation for the input
	Default     *cty.Value // Optional default value (nil means required)
}

// Workflow represents a workflow configuration with multiple tasks
type Workflow struct {
	Name            string          `hcl:"name,label"`
	SupervisorModel string          `hcl:"supervisor_model"`
	Agents          []string        `hcl:"agents"`
	Tasks           []Task          `hcl:"task,block"`
	Inputs          []WorkflowInput // Parsed from input blocks
}

// Task represents a single task within a workflow
type Task struct {
	Name          string         `hcl:"name,label"`
	ObjectiveExpr hcl.Expression // Stored for deferred evaluation with inputs
	Agents        []string       `hcl:"agents,optional"` // Optional - uses workflow-level agents if not specified
	DependsOn     []string       `hcl:"depends_on,optional"`
}

// Validate checks that the workflow configuration is valid
func (w *Workflow) Validate(models []Model, agents []Agent) error {
	if w.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	if w.SupervisorModel == "" {
		return fmt.Errorf("supervisor_model is required")
	}

	// Validate supervisor_model references a valid model
	if !isValidModelRef(w.SupervisorModel, models) {
		return fmt.Errorf("supervisor_model '%s' not found in models", w.SupervisorModel)
	}

	if len(w.Tasks) == 0 {
		return fmt.Errorf("workflow must have at least one task")
	}

	// Validate inputs
	inputNames := make(map[string]bool)
	for _, input := range w.Inputs {
		if inputNames[input.Name] {
			return fmt.Errorf("duplicate input name '%s'", input.Name)
		}
		inputNames[input.Name] = true
		if err := input.Validate(); err != nil {
			return fmt.Errorf("input '%s': %w", input.Name, err)
		}
	}

	// Build map of task names for dependency validation
	taskNames := make(map[string]bool)
	for _, t := range w.Tasks {
		if taskNames[t.Name] {
			return fmt.Errorf("duplicate task name '%s'", t.Name)
		}
		taskNames[t.Name] = true
	}

	// Build map of agent names for validation
	agentNames := make(map[string]bool)
	for _, a := range agents {
		agentNames[a.Name] = true
	}

	// Validate workflow-level agents
	for _, agentRef := range w.Agents {
		if !agentNames[agentRef] {
			return fmt.Errorf("agent '%s' not found", agentRef)
		}
	}

	// Validate each task
	for _, t := range w.Tasks {
		if err := t.Validate(taskNames, agentNames, w.Agents); err != nil {
			return fmt.Errorf("task '%s': %w", t.Name, err)
		}
	}

	// Validate DAG (no cycles)
	if err := w.ValidateDAG(); err != nil {
		return err
	}

	return nil
}

// Validate checks that the input configuration is valid
func (i *WorkflowInput) Validate() error {
	if i.Name == "" {
		return fmt.Errorf("input name is required")
	}
	validTypes := map[string]bool{
		InputTypeString: true,
		InputTypeNumber: true,
		InputTypeBool:   true,
		InputTypeList:   true,
		InputTypeObject: true,
	}
	if !validTypes[i.Type] {
		return fmt.Errorf("invalid type '%s': must be string, number, bool, list, or object", i.Type)
	}
	return nil
}

// BuildInputsCtyType returns the cty type for the inputs namespace
func (w *Workflow) BuildInputsCtyType() cty.Type {
	if len(w.Inputs) == 0 {
		return cty.EmptyObject
	}

	attrTypes := make(map[string]cty.Type)
	for _, input := range w.Inputs {
		attrTypes[input.Name] = inputTypeToCtyType(input.Type)
	}
	return cty.Object(attrTypes)
}

func inputTypeToCtyType(inputType string) cty.Type {
	switch inputType {
	case InputTypeString:
		return cty.String
	case InputTypeNumber:
		return cty.Number
	case InputTypeBool:
		return cty.Bool
	case InputTypeList:
		return cty.List(cty.DynamicPseudoType)
	case InputTypeObject:
		return cty.DynamicPseudoType
	default:
		return cty.String
	}
}

// ResolveInputValues converts string CLI values to cty.Values, applying defaults
func (w *Workflow) ResolveInputValues(provided map[string]string) (map[string]cty.Value, error) {
	result := make(map[string]cty.Value)

	for _, input := range w.Inputs {
		strVal, ok := provided[input.Name]
		if !ok {
			// Use default if available
			if input.Default != nil {
				result[input.Name] = *input.Default
				continue
			}
			return nil, fmt.Errorf("required input '%s' not provided", input.Name)
		}

		// Convert string to appropriate cty type
		ctyVal, err := parseInputValue(strVal, input.Type)
		if err != nil {
			return nil, fmt.Errorf("input '%s': %w", input.Name, err)
		}
		result[input.Name] = ctyVal
	}

	return result, nil
}

func parseInputValue(strVal string, inputType string) (cty.Value, error) {
	switch inputType {
	case InputTypeString:
		return cty.StringVal(strVal), nil
	case InputTypeNumber:
		f, err := strconv.ParseFloat(strVal, 64)
		if err != nil {
			return cty.NilVal, fmt.Errorf("invalid number: %w", err)
		}
		return cty.NumberFloatVal(f), nil
	case InputTypeBool:
		b, err := strconv.ParseBool(strVal)
		if err != nil {
			return cty.NilVal, fmt.Errorf("invalid bool: %w", err)
		}
		return cty.BoolVal(b), nil
	case InputTypeList:
		return parseJSONList(strVal)
	case InputTypeObject:
		return parseJSONObject(strVal)
	default:
		return cty.StringVal(strVal), nil
	}
}

func parseJSONList(strVal string) (cty.Value, error) {
	var items []interface{}
	if err := json.Unmarshal([]byte(strVal), &items); err != nil {
		return cty.NilVal, fmt.Errorf("invalid list (expected JSON array): %w", err)
	}
	if len(items) == 0 {
		return cty.ListValEmpty(cty.DynamicPseudoType), nil
	}
	ctyItems := make([]cty.Value, len(items))
	for i, item := range items {
		ctyItems[i] = goToCtyValue(item)
	}
	return cty.TupleVal(ctyItems), nil
}

func parseJSONObject(strVal string) (cty.Value, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(strVal), &obj); err != nil {
		return cty.NilVal, fmt.Errorf("invalid object (expected JSON object): %w", err)
	}
	if len(obj) == 0 {
		return cty.EmptyObjectVal, nil
	}
	ctyMap := make(map[string]cty.Value)
	for k, v := range obj {
		ctyMap[k] = goToCtyValue(v)
	}
	return cty.ObjectVal(ctyMap), nil
}

// goToCtyValue is defined in tool.go and reused here

// isValidModelRef checks if a model reference (e.g., "claude_sonnet_4") is valid
func isValidModelRef(modelRef string, models []Model) bool {
	for _, m := range models {
		for _, allowed := range m.AllowedModels {
			if allowed == modelRef {
				return true
			}
		}
	}
	return false
}

// Validate checks that the task configuration is valid
// workflowAgents are the agents defined at the workflow level
func (t *Task) Validate(taskNames map[string]bool, agentNames map[string]bool, workflowAgents []string) error {
	if t.Name == "" {
		return fmt.Errorf("task name is required")
	}

	if t.ObjectiveExpr == nil {
		return fmt.Errorf("objective is required")
	}

	// Task must have agents either at task level or workflow level
	if len(t.Agents) == 0 && len(workflowAgents) == 0 {
		return fmt.Errorf("no agents specified (neither at task nor workflow level)")
	}

	// Validate task-level agent references (if specified)
	for _, agentRef := range t.Agents {
		if !agentNames[agentRef] {
			return fmt.Errorf("agent '%s' not found", agentRef)
		}
	}

	// Validate depends_on references
	for _, dep := range t.DependsOn {
		if !taskNames[dep] {
			return fmt.Errorf("depends_on task '%s' not found", dep)
		}
		if dep == t.Name {
			return fmt.Errorf("task cannot depend on itself")
		}
	}

	return nil
}

// ResolvedObjective evaluates the objective expression with the given vars and inputs
func (t *Task) ResolvedObjective(vars map[string]cty.Value, inputs map[string]cty.Value) (string, error) {
	ctx := &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"vars":   cty.ObjectVal(vars),
			"inputs": cty.ObjectVal(inputs),
		},
	}
	val, diags := t.ObjectiveExpr.Value(ctx)
	if diags.HasErrors() {
		return "", fmt.Errorf("evaluating objective: %s", diags.Error())
	}
	return val.AsString(), nil
}

// ValidateDAG checks that the task dependencies form a valid DAG (no cycles)
func (w *Workflow) ValidateDAG() error {
	// Build adjacency list
	deps := make(map[string][]string)
	for _, t := range w.Tasks {
		deps[t.Name] = t.DependsOn
	}

	// Track visit state: 0=unvisited, 1=visiting (in stack), 2=visited
	state := make(map[string]int)

	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		if state[name] == 2 {
			return nil // Already fully visited
		}
		if state[name] == 1 {
			// Found a cycle - build cycle path for error message
			cyclePath := append(path, name)
			return fmt.Errorf("dependency cycle detected: %v", cyclePath)
		}

		state[name] = 1 // Mark as visiting
		for _, dep := range deps[name] {
			if err := visit(dep, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = 2 // Mark as visited
		return nil
	}

	// Visit all tasks
	for _, t := range w.Tasks {
		if err := visit(t.Name, nil); err != nil {
			return err
		}
	}

	return nil
}

// GetRootTasks returns tasks with no dependencies (can be executed immediately)
func (w *Workflow) GetRootTasks() []Task {
	var roots []Task
	for _, t := range w.Tasks {
		if len(t.DependsOn) == 0 {
			roots = append(roots, t)
		}
	}
	return roots
}

// GetTaskByName returns a task by name
func (w *Workflow) GetTaskByName(name string) *Task {
	for i := range w.Tasks {
		if w.Tasks[i].Name == name {
			return &w.Tasks[i]
		}
	}
	return nil
}

// TopologicalSort returns tasks in execution order (respecting dependencies)
func (w *Workflow) TopologicalSort() []Task {
	// Build in-degree map
	inDegree := make(map[string]int)
	for _, t := range w.Tasks {
		inDegree[t.Name] = len(t.DependsOn)
	}

	// Build reverse adjacency (which tasks depend on this one)
	dependents := make(map[string][]string)
	for _, t := range w.Tasks {
		for _, dep := range t.DependsOn {
			dependents[dep] = append(dependents[dep], t.Name)
		}
	}

	// Start with root tasks
	var queue []string
	for _, t := range w.Tasks {
		if inDegree[t.Name] == 0 {
			queue = append(queue, t.Name)
		}
	}

	var result []Task
	for len(queue) > 0 {
		// Pop from queue
		name := queue[0]
		queue = queue[1:]

		result = append(result, *w.GetTaskByName(name))

		// Decrease in-degree for dependents
		for _, dep := range dependents[name] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	return result
}
