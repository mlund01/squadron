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

// MissionInput represents an input parameter for a mission
type MissionInput struct {
	Name        string     // From HCL label
	Type        string     // "string", "number", "bool", "list", "object"
	Description string     // Documentation for the input
	Default     *cty.Value // Optional default value (nil means required for non-secrets)
	Secret      bool       // If true, value is opaque to LLM and uses ${secrets.name} in tool calls
	Value       *cty.Value // For secrets: the actual value (from vars.* or literal). Nil for regular inputs.
}

// Dataset represents a collection of items for task iteration
type Dataset struct {
	Name        string          // From HCL label
	Description string          // Documentation for the dataset
	BindTo      string          // Optional: input name to bind to (e.g., "cities" for inputs.cities)
	Schema      *InputsSchema   // Optional: schema for validating items
	Items       []cty.Value     // Optional: inline list of items
	BindToExpr  hcl.Expression  // Stored for deferred evaluation
}

// TaskIterator configures iteration over a dataset
type TaskIterator struct {
	Dataset          string // Dataset name (e.g., "city_list")
	Parallel         bool   // Default: false (sequential execution)
	MaxRetries       int    // Default: 0 (no retries). Max retry attempts per iteration on failure.
	ConcurrencyLimit int    // Default: 5. Max concurrent iterations when parallel=true.
	StartDelay       int    // Default: 0. Milliseconds delay between starts in first concurrent batch.
	Smoketest        bool   // Default: false. If true, run first iteration completely before starting others.
}

// OutputSchema defines the structured output for a task
type OutputSchema struct {
	Fields []OutputField
}

// OutputField represents a single output field definition
type OutputField struct {
	Name        string
	Type        string // string, number, integer, boolean
	Description string
	Required    bool
}

// Mission represents a mission configuration with multiple tasks
type Mission struct {
	Name            string          `hcl:"name,label"`
	SupervisorModel string          `hcl:"supervisor_model"`
	Agents          []string        `hcl:"agents"`
	Tasks           []Task          `hcl:"task,block"`
	Inputs          []MissionInput // Parsed from input blocks
	Datasets        []Dataset       // Parsed from dataset blocks
}

// Task represents a single task within a mission
type Task struct {
	Name          string         `hcl:"name,label"`
	ObjectiveExpr hcl.Expression // Stored for deferred evaluation with inputs
	Agents        []string       `hcl:"agents,optional"` // Optional - uses mission-level agents if not specified
	DependsOn     []string       `hcl:"depends_on,optional"`
	Iterator      *TaskIterator  // Optional: iterate over a dataset
	Output        *OutputSchema  // Optional: structured output schema
}

// Validate checks that the mission configuration is valid
func (w *Mission) Validate(models []Model, agents []Agent) error {
	if w.Name == "" {
		return fmt.Errorf("mission name is required")
	}

	if w.SupervisorModel == "" {
		return fmt.Errorf("supervisor_model is required")
	}

	// Validate supervisor_model references a valid model
	if !isValidModelRef(w.SupervisorModel, models) {
		return fmt.Errorf("supervisor_model '%s' not found in models", w.SupervisorModel)
	}

	if len(w.Tasks) == 0 {
		return fmt.Errorf("mission must have at least one task")
	}

	// Validate inputs
	inputNames := make(map[string]bool)
	inputTypes := make(map[string]string)
	for _, input := range w.Inputs {
		if inputNames[input.Name] {
			return fmt.Errorf("duplicate input name '%s'", input.Name)
		}
		inputNames[input.Name] = true
		inputTypes[input.Name] = input.Type
		if err := input.Validate(); err != nil {
			return fmt.Errorf("input '%s': %w", input.Name, err)
		}
	}

	// Validate datasets
	datasetNames := make(map[string]bool)
	for _, ds := range w.Datasets {
		if datasetNames[ds.Name] {
			return fmt.Errorf("duplicate dataset name '%s'", ds.Name)
		}
		datasetNames[ds.Name] = true
		if err := ds.Validate(); err != nil {
			return fmt.Errorf("dataset '%s': %w", ds.Name, err)
		}
		// Validate bind_to references a list-type input
		if ds.BindTo != "" {
			if !inputNames[ds.BindTo] {
				return fmt.Errorf("dataset '%s': bind_to references unknown input '%s'", ds.Name, ds.BindTo)
			}
			if inputTypes[ds.BindTo] != InputTypeList {
				return fmt.Errorf("dataset '%s': bind_to references input '%s' which is not type list", ds.Name, ds.BindTo)
			}
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

	// Validate mission-level agents
	for _, agentRef := range w.Agents {
		if !agentNames[agentRef] {
			return fmt.Errorf("agent '%s' not found", agentRef)
		}
	}

	// Validate each task
	for _, t := range w.Tasks {
		if err := t.Validate(taskNames, agentNames, datasetNames, w.Agents); err != nil {
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
func (i *MissionInput) Validate() error {
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

	// Secret inputs have additional requirements
	if i.Secret {
		// Secrets must have a value (from vars.* or literal)
		if i.Value == nil || i.Value.IsNull() {
			return fmt.Errorf("secret input must have a value")
		}
		// Secret values must be strings
		if i.Value.Type() != cty.String {
			return fmt.Errorf("secret value must be a string, got %s", i.Value.Type().FriendlyName())
		}
	}

	return nil
}

// Validate checks that the dataset configuration is valid
func (d *Dataset) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("dataset name is required")
	}
	// Datasets can have bind_to, default, or neither (populated dynamically via set_dataset tool)
	return nil
}

// ValidateItem validates a single item against the dataset schema
func (d *Dataset) ValidateItem(item cty.Value) error {
	if d.Schema == nil {
		return nil // No schema = any item accepted
	}

	// Item must be an object if schema is defined
	if !item.Type().IsObjectType() && !item.Type().IsMapType() {
		return fmt.Errorf("item must be an object when schema is defined")
	}

	// Validate each field in schema
	for _, field := range d.Schema.Fields {
		// Check if field exists in item
		if item.Type().IsObjectType() {
			if !item.Type().HasAttribute(field.Name) {
				if field.Required {
					return fmt.Errorf("required field '%s' is missing", field.Name)
				}
				continue
			}
		}

		// For map types, check via GetAttr
		fieldVal := item.GetAttr(field.Name)
		if fieldVal.IsNull() && field.Required {
			return fmt.Errorf("required field '%s' is null", field.Name)
		}

		// Type validation
		if !fieldVal.IsNull() {
			expectedType := stringToCtyType(field.Type)
			if !fieldVal.Type().Equals(expectedType) && expectedType != cty.DynamicPseudoType {
				// Allow number to match any numeric type
				if field.Type == "number" && fieldVal.Type() == cty.Number {
					continue
				}
				return fmt.Errorf("field '%s' has wrong type: expected %s", field.Name, field.Type)
			}
		}
	}

	return nil
}

// Validate checks that the task iterator configuration is valid
func (ti *TaskIterator) Validate() error {
	if ti.Dataset == "" {
		return fmt.Errorf("iterator dataset is required")
	}
	return nil
}

// BuildInputsCtyType returns the cty type for the inputs namespace
func (w *Mission) BuildInputsCtyType() cty.Type {
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

// ResolveInputValues converts string CLI values to cty.Values, applying defaults.
// Secret inputs are skipped - they get their value from the 'value' attribute
// and cannot be interpolated in objectives.
func (w *Mission) ResolveInputValues(provided map[string]string) (map[string]cty.Value, error) {
	result := make(map[string]cty.Value)

	for _, input := range w.Inputs {
		// Skip secret inputs - they are handled separately and not interpolatable
		if input.Secret {
			continue
		}

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
		ctyItems[i] = GoToCtyValue(item)
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
		ctyMap[k] = GoToCtyValue(v)
	}
	return cty.ObjectVal(ctyMap), nil
}

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
// missionAgents are the agents defined at the mission level
func (t *Task) Validate(taskNames map[string]bool, agentNames map[string]bool, datasetNames map[string]bool, missionAgents []string) error {
	if t.Name == "" {
		return fmt.Errorf("task name is required")
	}

	if t.ObjectiveExpr == nil {
		return fmt.Errorf("objective is required")
	}

	// Task must have agents either at task level or mission level
	if len(t.Agents) == 0 && len(missionAgents) == 0 {
		return fmt.Errorf("no agents specified (neither at task nor mission level)")
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

	// Validate iterator if present
	if t.Iterator != nil {
		if err := t.Iterator.Validate(); err != nil {
			return fmt.Errorf("iterator: %w", err)
		}
		if !datasetNames[t.Iterator.Dataset] {
			return fmt.Errorf("iterator references unknown dataset '%s'", t.Iterator.Dataset)
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
func (w *Mission) ValidateDAG() error {
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
func (w *Mission) GetRootTasks() []Task {
	var roots []Task
	for _, t := range w.Tasks {
		if len(t.DependsOn) == 0 {
			roots = append(roots, t)
		}
	}
	return roots
}

// GetTaskByName returns a task by name
func (w *Mission) GetTaskByName(name string) *Task {
	for i := range w.Tasks {
		if w.Tasks[i].Name == name {
			return &w.Tasks[i]
		}
	}
	return nil
}

// TopologicalSort returns tasks in execution order (respecting dependencies)
func (w *Mission) TopologicalSort() []Task {
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
