package config

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// Input type constants
const (
	InputTypeString  = "string"
	InputTypeNumber  = "number"
	InputTypeInteger = "integer"
	InputTypeBool    = "bool"
	InputTypeList    = "list"
	InputTypeObject  = "object"
	InputTypeMap     = "map"
	InputTypeFile    = "file"
)

// InputFileSlotPrefix is the namespace prefix under which file-typed mission
// inputs are registered in the MemoryStore. Agents address them as
// "input.<name>" via the `slot` parameter on every file tool. Mirrored in
// aitools.InputFileSlotPrefix; both must stay in sync.
const InputFileSlotPrefix = "input."

// MissionInput represents an input parameter for a mission
type MissionInput struct {
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	Default     *cty.Value     `json:"-"`
	Protected   bool           `json:"protected,omitempty"`
	Value       *cty.Value     `json:"-"`
	Items       *MissionInput  `json:"items,omitempty"`      // Element type for list/map
	Properties  []MissionInput `json:"properties,omitempty"` // Nested fields for object
}

// Dataset represents a collection of items for task iteration
type Dataset struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	BindTo      string         `json:"bindTo,omitempty"`
	Schema      *InputsSchema  `json:"schema,omitempty"`
	Items       []cty.Value    `json:"-"`
	BindToExpr  hcl.Expression `json:"-"`
}

// TaskIterator configures iteration over a dataset
type TaskIterator struct {
	Dataset          string `json:"dataset"`                    // Dataset name (e.g., "city_list")
	Parallel         bool   `json:"parallel"`                   // Default: false (sequential execution)
	MaxRetries       int    `json:"maxRetries,omitempty"`       // Default: 0 (no retries). Max retry attempts per iteration on failure.
	ConcurrencyLimit int    `json:"concurrencyLimit,omitempty"` // Default: 5. Max concurrent iterations when parallel=true.
	StartDelay       int    `json:"startDelay,omitempty"`       // Default: 0. Milliseconds delay between starts in first concurrent batch.
	Smoketest        bool   `json:"smoketest,omitempty"`        // Default: false. If true, run first iteration completely before starting others.
}

// OutputSchema defines the structured output for a task
type OutputSchema struct {
	Fields []OutputField `json:"fields"`
}

// OutputField represents a single output field definition.
// For list/map types, Items holds the element type descriptor.
// For object types, Properties holds the nested field definitions.
type OutputField struct {
	Name        string        `json:"name"`
	Type        string        `json:"type"` // string, number, integer, boolean, array, object
	Description string        `json:"description,omitempty"`
	Required    bool          `json:"required,omitempty"`
	Items       *OutputField  `json:"items,omitempty"`
	Properties  []OutputField `json:"properties,omitempty"`
}

// (Memory and MissionMemory live in memory.go.)

// Trigger defines a webhook-based trigger for a mission.
type Trigger struct {
	WebhookPath string `hcl:"webhook_path,optional" json:"webhookPath,omitempty"` // Defaults to "/<mission_name>" if empty
	Secret      string `hcl:"secret,optional" json:"secret,omitempty"`            // Optional: validates X-Webhook-Secret header
}

// CommanderPruning configures context pruning for a commander
type CommanderPruning struct {
	// PruneOn: trigger pruning when conversation reaches this many turns (0 = disabled)
	PruneOn int `hcl:"prune_on"`
	// PruneTo: when pruning triggers, reduce conversation to this many turns
	PruneTo int `hcl:"prune_to"`
}

// MissionCommander holds configuration for the mission's commander LLM
type MissionCommander struct {
	Model        string              `json:"model"`
	Compaction   *Compaction         `json:"compaction,omitempty"`
	Pruning      *CommanderPruning   `json:"pruning,omitempty"`
	ToolResponse *ToolResponseConfig `json:"toolResponse,omitempty"`
	// Reasoning controls native provider reasoning for the commander.
	// Valid values: "", "low", "medium", "high". Silently no-op on models
	// that don't support native reasoning.
	Reasoning string `json:"reasoning,omitempty"`
}

// GetToolResponseMaxBytes returns the configured max size in bytes for tool responses, falling back to default.
func (c *MissionCommander) GetToolResponseMaxBytes() int {
	if c == nil || c.ToolResponse == nil || c.ToolResponse.MaxTokens <= 0 {
		return DefaultToolResponseMaxTokens * bytesPerToken
	}
	tokens := c.ToolResponse.MaxTokens
	if tokens > HardMaxToolResponseTokens {
		tokens = HardMaxToolResponseTokens
	}
	return tokens * bytesPerToken
}

// Mission represents a mission configuration with multiple tasks
type Mission struct {
	Name      string            `hcl:"name,label"`
	Directive string            `hcl:"directive,optional"`
	Source    *ConfigSource     `hcl:"-" json:"-"`
	Commander *MissionCommander `json:"-"` // Parsed manually from commander block
	// Models is the mission-level model pool that the commander may assign to
	// task executors. It is intentionally separate from the commander's model.
	Models []string `json:"models,omitempty"`
	// Tools is the mission-level capability pool that the commander may grant
	// to task executors. The commander never receives these tools itself.
	Tools        []string            `json:"tools,omitempty"`
	Agents       []string            `hcl:"agents"`
	LocalAgents  []Agent             `json:"localAgents,omitempty"` // Mission-scoped agents
	Tasks        []Task              `hcl:"task,block"`
	Inputs       []MissionInput      // Parsed from input blocks
	Datasets     []Dataset           // Parsed from dataset blocks
	Memories     []string            // Shared memory names referenced by this mission
	Packets      []string            // Packet names referenced by this mission (read-only reference data bundles)
	Memory       *MissionMemory      // Optional persistent mission memory (slot "memory")
	Scratchpad   bool                // If true, mission gets an ephemeral per-run scratchpad (slot "scratchpad")
	Trigger      *Trigger            `json:"trigger,omitempty"`
	MaxParallel  int                 `json:"maxParallel,omitempty"` // default 3
	Budget       *Budget             `json:"budget,omitempty"`
	Notification *NotificationConfig `json:"notification,omitempty"` // opt-in terminal-event notifications
}

// GetLocalAgent returns a mission-scoped agent by name, or nil if not found.
func (m *Mission) GetLocalAgent(name string) *Agent {
	for i := range m.LocalAgents {
		if m.LocalAgents[i].Name == name {
			return &m.LocalAgents[i]
		}
	}
	return nil
}

// Task represents a single task within a mission
type Task struct {
	Name          string         `hcl:"name,label" json:"name"`
	ObjectiveExpr hcl.Expression `json:"-"`
	RawObjective  string         `json:"rawObjective,omitempty"` // Raw objective text from HCL source (with ${...} placeholders intact)
	Agents        []string       `hcl:"agents,optional" json:"agents,omitempty"`
	// Models narrows the mission model pool for this task. When omitted, the
	// executor may use any model declared by the mission.
	Models []string `json:"models,omitempty"`
	// Tools are capabilities always granted to this task executor. Additional
	// tools may be selected from the mission-level tool pool at dispatch time.
	Tools             []string      `json:"tools,omitempty"`
	MaxRetries        int           `json:"maxRetries,omitempty"`
	RetryDelaySeconds int           `json:"retryDelaySeconds,omitempty"`
	TimeoutSeconds    int           `json:"timeoutSeconds,omitempty"`
	Packets           []string      `json:"packets,omitempty"` // task-scoped declared packet references (parsed manually)
	DependsOn         []string      `hcl:"depends_on,optional" json:"dependsOn,omitempty"`
	Iterator          *TaskIterator `json:"iterator,omitempty"`
	Output            *OutputSchema `json:"output,omitempty"`
	Router            *TaskRouter   `json:"router,omitempty"`
	SendTo            []string      `json:"sendTo,omitempty"`
	Budget            *Budget       `json:"budget,omitempty"`
}

// HasExecutorPolicy reports whether the mission declares model or tool policy
// for its task executors.
func (m *Mission) HasExecutorPolicy() bool {
	if len(m.Models) > 0 || len(m.Tools) > 0 {
		return true
	}
	for _, task := range m.Tasks {
		if len(task.Models) > 0 || len(task.Tools) > 0 {
			return true
		}
	}
	return false
}

// TaskRouter defines conditional routing after task completion
type TaskRouter struct {
	Routes []TaskRoute `json:"routes"`
}

// TaskRoute defines a single conditional route
type TaskRoute struct {
	Target    string `json:"target"`    // task name or mission name
	Condition string `json:"condition"` // natural language condition for the LLM to evaluate
	IsMission bool   `json:"isMission"` // true if target is a mission reference (missions.foo)
}

// Validate checks that the mission configuration is valid
func (w *Mission) Validate(models []Model, memories []Memory, packets []Packet, allMissionNames map[string]bool) error {
	if err := validateSlotName(w.Name); err != nil {
		return fmt.Errorf("mission name: %w", err)
	}

	if w.Commander == nil || w.Commander.Model == "" {
		return fmt.Errorf("commander is required")
	}

	// Validate commander references a valid model
	if !isValidModelRef(w.Commander.Model, models) {
		return fmt.Errorf("commander '%s' not found in models", w.Commander.Model)
	}

	if len(w.Agents) > 0 || len(w.LocalAgents) > 0 {
		return fmt.Errorf("missions cannot reference managed agents; declare executor models and tools on the mission or task")
	}
	for _, task := range w.Tasks {
		if len(task.Agents) > 0 {
			return fmt.Errorf("mission task %q cannot reference managed agents", task.Name)
		}
	}
	hasExecutorPolicy := w.HasExecutorPolicy()
	if !hasExecutorPolicy {
		return fmt.Errorf("mission must declare task-executor models")
	}
	for _, modelRef := range w.Models {
		if !isValidModelRef(modelRef, models) {
			return fmt.Errorf("executor model '%s' not found in models", modelRef)
		}
	}

	// Validate compaction settings if present
	if w.Commander.Compaction != nil {
		if w.Commander.Compaction.TokenLimit <= 0 {
			return fmt.Errorf("commander compaction token_limit must be > 0")
		}
	}

	// Validate pruning settings if present
	if w.Commander.Pruning != nil {
		if w.Commander.Pruning.PruneOn <= 0 {
			return fmt.Errorf("commander pruning prune_on must be > 0")
		}
		if w.Commander.Pruning.PruneTo <= 0 {
			return fmt.Errorf("commander pruning prune_to must be > 0")
		}
		if w.Commander.Pruning.PruneTo >= w.Commander.Pruning.PruneOn {
			return fmt.Errorf("commander pruning prune_to must be less than prune_on")
		}
	}

	// Validate commander reasoning level
	if normalized, err := NormalizeReasoning(w.Commander.Reasoning); err != nil {
		return fmt.Errorf("commander: %w", err)
	} else {
		w.Commander.Reasoning = normalized
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

	// Validate shared memory references
	memoryNames := make(map[string]bool)
	for _, m := range memories {
		memoryNames[m.Name] = true
	}
	for _, ref := range w.Memories {
		if !memoryNames[ref] {
			return fmt.Errorf("shared memory '%s' not found", ref)
		}
	}

	// Validate packet references (mission-level + per-task).
	packetNames := make(map[string]bool)
	for _, p := range packets {
		packetNames[p.Name] = true
	}
	for _, ref := range w.Packets {
		if !packetNames[ref] {
			return fmt.Errorf("packet '%s' not found", ref)
		}
	}
	for _, t := range w.Tasks {
		for _, ref := range t.Packets {
			if !packetNames[ref] {
				return fmt.Errorf("task '%s': packet '%s' not found", t.Name, ref)
			}
		}
	}

	// Validate the mission memory block if present.
	if w.Memory != nil {
		if err := w.Memory.Validate(); err != nil {
			return fmt.Errorf("memory: %w", err)
		}
	}

	// Validate each task
	for _, t := range w.Tasks {
		if err := t.Validate(taskNames, datasetNames, w.Models, models, allMissionNames); err != nil {
			return fmt.Errorf("task '%s': %w", t.Name, err)
		}
	}

	// Validate router constraints at mission level
	routerTargets := w.GetRouterTargets()

	// Routed-to tasks cannot have depends_on
	for targetName := range routerTargets {
		target := w.GetTaskByName(targetName)
		if target != nil && len(target.DependsOn) > 0 {
			return fmt.Errorf("task '%s': dynamically activated tasks cannot have depends_on", targetName)
		}
	}

	// No task can depend on a dynamically activated task (router/send_to targets
	// are the roots of their own sub-DAGs and cannot be depended on)
	for _, t := range w.Tasks {
		for _, dep := range t.DependsOn {
			if _, isDynamic := routerTargets[dep]; isDynamic {
				return fmt.Errorf("task '%s': cannot depend on '%s' because it is dynamically activated (via router/send_to)", t.Name, dep)
			}
		}
	}

	// Validate at least one task can start (has no depends_on and is not router-only)
	if len(routerTargets) > 0 {
		hasStartTask := false
		for _, t := range w.Tasks {
			if len(t.DependsOn) == 0 && !w.IsRouterOnlyTask(t.Name) {
				hasStartTask = true
				break
			}
		}
		if !hasStartTask {
			return fmt.Errorf("mission must have at least one task that can start (no depends_on and not only reachable via router)")
		}
	}

	// Validate DAG (no cycles — includes router edges)
	if err := w.ValidateDAG(); err != nil {
		return err
	}

	if err := w.validateNoTransitiveDeps(); err != nil {
		return err
	}

	// Validate trigger
	if w.Trigger != nil {
		if w.Trigger.WebhookPath == "" {
			w.Trigger.WebhookPath = "/" + w.Name
		}
	}

	// Validate max_parallel
	if w.MaxParallel < 0 {
		return fmt.Errorf("max_parallel must be >= 1")
	}

	// Validate budget
	if err := w.Budget.Validate(); err != nil {
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
		InputTypeString:  true,
		InputTypeNumber:  true,
		InputTypeInteger: true,
		InputTypeBool:    true,
		InputTypeList:    true,
		InputTypeObject:  true,
		InputTypeMap:     true,
		InputTypeFile:    true,
	}
	if !validTypes[i.Type] {
		return fmt.Errorf("invalid type '%s': must be string, number, integer, bool, list, map, object, or file", i.Type)
	}

	// Integer defaults must be whole numbers
	if i.Type == InputTypeInteger && i.Default != nil {
		bf := i.Default.AsBigFloat()
		if !bf.IsInt() {
			return fmt.Errorf("input %q: default value must be a whole number for integer type", i.Name)
		}
	}

	// File inputs carry their bytes at run time (a path or a base64 upload),
	// so a compile-time default makes no sense, and they're never secrets —
	// the agent reads the file through a read-only slot, never an objective.
	if i.Type == InputTypeFile {
		if i.Default != nil {
			return fmt.Errorf("input %q: file inputs cannot have a default", i.Name)
		}
		if i.Protected {
			return fmt.Errorf("input %q: file inputs cannot be protected", i.Name)
		}
	}

	// Protected inputs have additional requirements
	if i.Protected {
		// Protected inputs must have a value (from vars.* or literal)
		if i.Value == nil || i.Value.IsNull() {
			return fmt.Errorf("protected input must have a value")
		}
		// Protected values must be strings
		if i.Value.Type() != cty.String {
			return fmt.Errorf("protected value must be a string, got %s", i.Value.Type().FriendlyName())
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
	case InputTypeNumber, InputTypeInteger:
		return cty.Number
	case InputTypeBool:
		return cty.Bool
	case InputTypeFile:
		// A file input interpolates into objectives as its slot name string
		// (e.g. "input.document"); the bytes live in the read-only slot.
		return cty.String
	case InputTypeList:
		return cty.List(cty.DynamicPseudoType)
	case InputTypeObject, InputTypeMap:
		return cty.DynamicPseudoType
	default:
		return cty.String
	}
}

// ResolveInputValues converts string CLI values to cty.Values, applying defaults.
// Protected inputs are skipped - they get their value from the 'value' attribute
// and cannot be interpolated in objectives.
func (w *Mission) ResolveInputValues(provided map[string]string) (map[string]cty.Value, error) {
	result := make(map[string]cty.Value)

	for _, input := range w.Inputs {
		// Skip protected inputs - they are handled separately and not interpolatable
		if input.Protected {
			continue
		}

		// File inputs surface to objectives as their slot name; the bytes are
		// materialized into the input.<name> slot by the mission runner. The
		// value must be provided (a path or a base64 upload) — file inputs
		// have no default.
		if input.Type == InputTypeFile {
			if _, ok := provided[input.Name]; !ok {
				return nil, fmt.Errorf("required input '%s' not provided", input.Name)
			}
			result[input.Name] = cty.StringVal(InputFileSlotPrefix + input.Name)
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

// ValidateValue checks a supplied route input with the same parser used at
// mission startup, so invalid values can be corrected before the sender completes.
func (i MissionInput) ValidateValue(value string) error {
	_, err := parseInputValue(value, i.Type)
	return err
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
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return cty.NilVal, fmt.Errorf("invalid number: must be finite")
		}
		return cty.NumberFloatVal(f), nil
	case InputTypeInteger:
		f, err := strconv.ParseFloat(strVal, 64)
		if err != nil {
			return cty.NilVal, fmt.Errorf("invalid integer: %w", err)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return cty.NilVal, fmt.Errorf("invalid integer: must be finite")
		}
		bf := new(big.Float).SetFloat64(f)
		if !bf.IsInt() {
			return cty.NilVal, fmt.Errorf("invalid integer: %q is not a whole number", strVal)
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
	case InputTypeObject, InputTypeMap:
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
		if _, ok := m.AvailableModels()[modelRef]; ok {
			return true
		}
	}
	return false
}

// Validate checks that the task configuration is valid
func (t *Task) Validate(taskNames map[string]bool, datasetNames map[string]bool, missionModels []string, configuredModels []Model, allMissionNames map[string]bool) error {
	if t.Name == "" {
		return fmt.Errorf("task name is required")
	}

	if t.ObjectiveExpr == nil {
		return fmt.Errorf("objective is required")
	}

	allowedModels := missionModels
	if len(t.Models) > 0 {
		allowedModels = t.Models
	}
	if len(allowedModels) == 0 {
		return fmt.Errorf("no executor models specified (neither at task nor mission level)")
	}
	missionModelSet := make(map[string]bool, len(missionModels))
	for _, modelRef := range missionModels {
		missionModelSet[modelRef] = true
	}
	for _, modelRef := range t.Models {
		if !isValidModelRef(modelRef, configuredModels) {
			return fmt.Errorf("executor model '%s' not found in models", modelRef)
		}
		if len(missionModelSet) > 0 && !missionModelSet[modelRef] {
			return fmt.Errorf("executor model '%s' is not in the mission model pool", modelRef)
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

	// send_to and router are mutually exclusive
	if len(t.SendTo) > 0 && t.Router != nil {
		return fmt.Errorf("task cannot have both send_to and router")
	}

	// Validate send_to if present
	if len(t.SendTo) > 0 {
		seen := make(map[string]bool)
		for _, target := range t.SendTo {
			if target == t.Name {
				return fmt.Errorf("send_to: task cannot send to itself")
			}
			if !taskNames[target] {
				return fmt.Errorf("send_to: task '%s' not found", target)
			}
			if seen[target] {
				return fmt.Errorf("send_to: duplicate target '%s'", target)
			}
			seen[target] = true
		}
	}

	// Validate budget if present
	if err := t.Budget.Validate(); err != nil {
		return err
	}

	// Validate router if present
	if t.Router != nil {
		if len(t.Router.Routes) == 0 {
			return fmt.Errorf("router must have at least one route")
		}
		// Cannot have router with parallel iterator
		if t.Iterator != nil && t.Iterator.Parallel {
			return fmt.Errorf("parallel iterators cannot have a router")
		}
		seenTargets := make(map[string]bool)
		for i, route := range t.Router.Routes {
			if route.Target == "" {
				return fmt.Errorf("route %d: target is required", i+1)
			}
			if route.Condition == "" {
				return fmt.Errorf("route %d: condition is required", i+1)
			}
			if route.Target == t.Name {
				return fmt.Errorf("route %d: task cannot route to itself", i+1)
			}
			if seenTargets[route.Target] {
				return fmt.Errorf("route %d: duplicate target '%s'", i+1, route.Target)
			}
			seenTargets[route.Target] = true
			if route.IsMission {
				if !allMissionNames[route.Target] {
					return fmt.Errorf("route %d: mission '%s' not found", i+1, route.Target)
				}
			} else {
				if !taskNames[route.Target] {
					return fmt.Errorf("route %d: task '%s' not found", i+1, route.Target)
				}
			}
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

// GetRouterTargets returns a map of target task name → list of task names that target it
// (via router routes or send_to). Mission route targets are excluded since they are not local tasks.
func (w *Mission) GetRouterTargets() map[string][]string {
	targets := make(map[string][]string)
	for _, t := range w.Tasks {
		if t.Router != nil {
			for _, route := range t.Router.Routes {
				if !route.IsMission {
					targets[route.Target] = append(targets[route.Target], t.Name)
				}
			}
		}
		for _, target := range t.SendTo {
			targets[target] = append(targets[target], t.Name)
		}
	}
	return targets
}

// IsRouterOnlyTask returns true if a task is only reachable via a router
// (it is a router target and has no depends_on)
func (w *Mission) IsRouterOnlyTask(name string) bool {
	task := w.GetTaskByName(name)
	if task == nil {
		return false
	}
	if len(task.DependsOn) > 0 {
		return false
	}
	targets := w.GetRouterTargets()
	_, isTarget := targets[name]
	return isTarget
}

// validateNoTransitiveDeps rejects depends_on lists that include a task
// already reachable through another listed dependency. depends_on must list
// only direct predecessors so the declared graph stays in lockstep with the
// effective execution order.
func (w *Mission) validateNoTransitiveDeps() error {
	direct := make(map[string][]string, len(w.Tasks))
	for _, t := range w.Tasks {
		if len(t.DependsOn) > 0 {
			direct[t.Name] = t.DependsOn
		}
	}

	ancestors := make(map[string]map[string]bool, len(w.Tasks))
	var ancestorsOf func(name string) map[string]bool
	ancestorsOf = func(name string) map[string]bool {
		if cached, ok := ancestors[name]; ok {
			return cached
		}
		out := make(map[string]bool)
		for _, dep := range direct[name] {
			out[dep] = true
			for ancestor := range ancestorsOf(dep) {
				out[ancestor] = true
			}
		}
		ancestors[name] = out
		return out
	}

	for _, t := range w.Tasks {
		if len(t.DependsOn) < 2 {
			continue
		}
		for i, depA := range t.DependsOn {
			for j, depB := range t.DependsOn {
				if i == j {
					continue
				}
				if ancestorsOf(depB)[depA] {
					return fmt.Errorf(
						"task '%s': depends_on includes '%s', which is already a transitive dependency through '%s' — list only direct predecessors",
						t.Name, depA, depB,
					)
				}
			}
		}
	}
	return nil
}

// ValidateDAG checks that the task dependencies form a valid DAG (no cycles)
// This includes both depends_on edges and router edges (task → route target)
func (w *Mission) ValidateDAG() error {
	// Build adjacency list: depends_on edges (reversed: dep → dependent)
	// plus router edges (router task → route target)
	deps := make(map[string][]string)
	for _, t := range w.Tasks {
		// depends_on edges
		deps[t.Name] = append(deps[t.Name], t.DependsOn...)
		// Router edges: the routing task points to its targets (local tasks only)
		if t.Router != nil {
			for _, route := range t.Router.Routes {
				if !route.IsMission {
					deps[t.Name] = append(deps[t.Name], route.Target)
				}
			}
		}
		// send_to edges: task points to its targets
		for _, target := range t.SendTo {
			deps[t.Name] = append(deps[t.Name], target)
		}
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
