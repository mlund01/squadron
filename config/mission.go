package config

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/robfig/cron/v3"
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
)

// MissionInput represents an input parameter for a mission
type MissionInput struct {
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Description string          `json:"description,omitempty"`
	Default     *cty.Value      `json:"-"`
	Protected   bool            `json:"protected,omitempty"`
	Value       *cty.Value      `json:"-"`
	Items       *MissionInput   `json:"items,omitempty"`       // Element type for list/map
	Properties  []MissionInput  `json:"properties,omitempty"`  // Nested fields for object
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
	Type        string        `json:"type"`                  // string, number, integer, boolean, array, object
	Description string        `json:"description,omitempty"`
	Required    bool          `json:"required,omitempty"`
	Items       *OutputField  `json:"items,omitempty"`
	Properties  []OutputField `json:"properties,omitempty"`
}

// MissionFolder represents a dedicated folder for a mission.
// Registered under the reserved name "mission". Persists across runs.
type MissionFolder struct {
	Path        string `hcl:"path"`
	Description string `hcl:"description,optional"`
}

// Validate checks that the mission folder configuration is valid
func (mf *MissionFolder) Validate() error {
	if mf.Path == "" {
		return fmt.Errorf("path is required")
	}
	return nil
}

// DefaultRunFolderCleanupDays is the auto-delete window applied when a
// run_folder block does not specify `cleanup`.
const DefaultRunFolderCleanupDays = 7

// MissionRunFolder represents a per-run ephemeral folder for a mission.
// Registered under the reserved name "run". A fresh subdirectory is created
// under Base for each mission run, keyed by mission ID.
//
// Cleanup is a pointer so we can distinguish "user didn't set it" (apply
// default of 7 days) from "user set 0" (keep forever).
type MissionRunFolder struct {
	Base        string `hcl:"base,optional"`        // parent directory; defaults to ".squadron/runs"
	Description string `hcl:"description,optional"`
	Cleanup     *int   `hcl:"cleanup,optional"`     // days after creation before auto-delete; nil = default (7), 0 = never
}

// Validate rejects negative cleanup values. Default-filling happens at parse
// time (see config.go) so callers reading a parsed Mission see a complete
// struct without needing to validate first.
func (rf *MissionRunFolder) Validate() error {
	if rf.Cleanup != nil && *rf.Cleanup < 0 {
		return fmt.Errorf("cleanup must be >= 0 (days)")
	}
	return nil
}

// Schedule defines a time-based trigger for a mission.
// Three modes (mutually exclusive):
//   - at:    specific times of day, e.g. ["09:00", "17:00"]
//   - every: repeating interval that divides evenly into 60m or 24h, e.g. "5m", "2h"
//   - cron:  raw 5-field cron expression
//
// weekdays and timezone can be combined with at or every.
type Schedule struct {
	At       []string          `hcl:"at,optional" json:"at,omitempty"`             // Time(s) of day: "09:00", "17:00" (24h format)
	Every    string            `hcl:"every,optional" json:"every,omitempty"`       // Interval: "5m", "15m", "1h", "2h", "4h", "6h", "12h"
	Weekdays []string          `hcl:"weekdays,optional" json:"weekdays,omitempty"` // Day filter: "mon", "tue", etc.
	Cron     string            `hcl:"cron,optional" json:"cron,omitempty"`         // 5-field cron expression
	Timezone string            `hcl:"timezone,optional" json:"timezone,omitempty"` // IANA timezone, defaults to system local
	Inputs   map[string]string `json:"inputs,omitempty"`                           // Input values to pass when firing (parsed manually from HCL)
}

// validWeekdays maps lowercase weekday abbreviations to true.
var validWeekdays = map[string]bool{
	"mon": true, "tue": true, "wed": true, "thu": true,
	"fri": true, "sat": true, "sun": true,
}

// weekdayCronMap maps weekday abbreviations to cron day-of-week values.
var weekdayCronMap = map[string]string{
	"sun": "0", "mon": "1", "tue": "2", "wed": "3",
	"thu": "4", "fri": "5", "sat": "6",
}

// timeOfDayPattern matches HH:MM in 24h format.
var timeOfDayPattern = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

// Validate checks that the schedule configuration is valid.
func (s *Schedule) Validate() error {
	hasAt := len(s.At) > 0
	hasEvery := s.Every != ""
	hasCron := s.Cron != ""

	modes := 0
	if hasAt {
		modes++
	}
	if hasEvery {
		modes++
	}
	if hasCron {
		modes++
	}
	if modes != 1 {
		return fmt.Errorf("exactly one of 'at', 'every', or 'cron' must be set")
	}

	if hasAt {
		for _, t := range s.At {
			if !timeOfDayPattern.MatchString(t) {
				return fmt.Errorf("invalid 'at' value %q: must be HH:MM (24h format)", t)
			}
		}
	}

	if hasEvery {
		if err := validateEveryInterval(s.Every); err != nil {
			return err
		}
	}

	if hasCron {
		if len(s.Weekdays) > 0 {
			return fmt.Errorf("'weekdays' cannot be used with 'cron'")
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(s.Cron); err != nil {
			return fmt.Errorf("invalid cron expression %q: %w", s.Cron, err)
		}
	}

	for _, wd := range s.Weekdays {
		if !validWeekdays[strings.ToLower(wd)] {
			return fmt.Errorf("invalid weekday %q: must be mon-sun", wd)
		}
	}

	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return fmt.Errorf("invalid timezone %q: %w", s.Timezone, err)
		}
	}

	return nil
}

// ToCron compiles the schedule into a 5-field cron expression.
// Panics if called on an invalid schedule (call Validate first).
func (s *Schedule) ToCron() string {
	if s.Cron != "" {
		return s.Cron
	}

	dow := "*"
	if len(s.Weekdays) > 0 {
		parts := make([]string, len(s.Weekdays))
		for i, wd := range s.Weekdays {
			parts[i] = weekdayCronMap[strings.ToLower(wd)]
		}
		dow = strings.Join(parts, ",")
	}

	if len(s.At) > 0 {
		// Collect unique hours and minutes
		minutes := make([]string, 0, len(s.At))
		hours := make([]string, 0, len(s.At))
		for _, t := range s.At {
			m := timeOfDayPattern.FindStringSubmatch(t)
			h, _ := strconv.Atoi(m[1])
			min, _ := strconv.Atoi(m[2])
			minutes = append(minutes, strconv.Itoa(min))
			hours = append(hours, strconv.Itoa(h))
		}
		// If all minutes are the same, use a single minute with multiple hours
		// Otherwise generate one cron per at-time... but cron doesn't support that easily.
		// For simplicity: if there's one at-time, emit "M H * * dow"
		// If multiple at-times share the same minute, emit "M H1,H2 * * dow"
		// Otherwise we need separate cron entries — for now, just emit first one
		// Actually, cron supports comma-separated hours+minutes combos:
		// We'll group by minute for cleaner expressions
		if len(s.At) == 1 {
			return fmt.Sprintf("%s %s * * %s", minutes[0], hours[0], dow)
		}
		// Multiple at-times: check if all have same minute
		allSameMin := true
		for _, min := range minutes {
			if min != minutes[0] {
				allSameMin = false
				break
			}
		}
		if allSameMin {
			return fmt.Sprintf("%s %s * * %s", minutes[0], strings.Join(hours, ","), dow)
		}
		// Different minutes: we can still express this as comma-separated if we
		// accept the cross-product limitation. For most use cases this is fine.
		return fmt.Sprintf("%s %s * * %s", strings.Join(minutes, ","), strings.Join(hours, ","), dow)
	}

	// every mode: parse interval and generate step syntax
	d, _ := time.ParseDuration(s.Every)
	totalMinutes := int(d.Minutes())
	if totalMinutes < 60 {
		// Sub-hour: */N * * * dow
		return fmt.Sprintf("*/%d * * * %s", totalMinutes, dow)
	}
	// Hourly or multi-hour: 0 */N * * dow
	totalHours := totalMinutes / 60
	return fmt.Sprintf("0 */%d * * %s", totalHours, dow)
}

// validateEveryInterval checks that the every duration divides evenly into 60 minutes or 24 hours.
func validateEveryInterval(every string) error {
	d, err := time.ParseDuration(every)
	if err != nil {
		return fmt.Errorf("invalid 'every' interval %q: %w", every, err)
	}
	if d < time.Minute {
		return fmt.Errorf("'every' must be at least 1m, got %s", every)
	}

	totalMinutes := int(d.Minutes())
	if totalMinutes < 60 {
		// Must divide evenly into 60 minutes
		if 60%totalMinutes != 0 {
			return fmt.Errorf("'every' interval %q must divide evenly into 60 minutes (valid: 1m, 2m, 3m, 4m, 5m, 6m, 10m, 12m, 15m, 20m, 30m)", every)
		}
		return nil
	}

	totalHours := totalMinutes / 60
	if totalMinutes%60 != 0 {
		return fmt.Errorf("'every' interval %q must be a whole number of hours when >= 1h", every)
	}
	// Must divide evenly into 24 hours
	if 24%totalHours != 0 {
		return fmt.Errorf("'every' interval %q must divide evenly into 24 hours (valid: 1h, 2h, 3h, 4h, 6h, 8h, 12h)", every)
	}
	return nil
}

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
	Name        string            `hcl:"name,label"`
	Directive   string            `hcl:"directive,optional"`
	Commander   *MissionCommander `json:"-"` // Parsed manually from commander block
	Agents      []string          `hcl:"agents"`
	LocalAgents []Agent           `json:"localAgents,omitempty"` // Mission-scoped agents
	Tasks       []Task            `hcl:"task,block"`
	Inputs      []MissionInput    // Parsed from input blocks
	Datasets    []Dataset         // Parsed from dataset blocks
	Folders     []string            // Shared folder names referenced by this mission
	Folder      *MissionFolder      // Optional dedicated mission folder (reserved name "mission")
	RunFolder   *MissionRunFolder   // Optional per-run ephemeral folder (reserved name "run")
	Schedules   []Schedule        `json:"schedules,omitempty"`
	Trigger     *Trigger          `json:"trigger,omitempty"`
	MaxParallel int               `json:"maxParallel,omitempty"` // default 3
	Budget      *Budget           `json:"budget,omitempty"`
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
	DependsOn     []string       `hcl:"depends_on,optional" json:"dependsOn,omitempty"`
	Iterator      *TaskIterator  `json:"iterator,omitempty"`
	Output        *OutputSchema  `json:"output,omitempty"`
	Router        *TaskRouter    `json:"router,omitempty"`
	SendTo        []string       `json:"sendTo,omitempty"`
	Budget        *Budget        `json:"budget,omitempty"`
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
func (w *Mission) Validate(models []Model, agents []Agent, sharedFolders []SharedFolder, allMissionNames map[string]bool) error {
	if w.Name == "" {
		return fmt.Errorf("mission name is required")
	}

	if w.Commander == nil || w.Commander.Model == "" {
		return fmt.Errorf("commander is required")
	}

	// Validate commander references a valid model
	if !isValidModelRef(w.Commander.Model, models) {
		return fmt.Errorf("commander '%s' not found in models", w.Commander.Model)
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

	// Validate mission-scoped (local) agents
	for i := range w.LocalAgents {
		if err := w.LocalAgents[i].Validate(); err != nil {
			return err
		}
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

	// Build map of agent names for validation (global + local)
	agentNames := make(map[string]bool)
	for _, a := range agents {
		agentNames[a.Name] = true
	}

	// Check for name conflicts between local and global agents
	for _, la := range w.LocalAgents {
		for _, ga := range agents {
			if la.Name == ga.Name {
				return fmt.Errorf("mission-scoped agent '%s' conflicts with global agent of the same name", la.Name)
			}
		}
		agentNames[la.Name] = true
	}

	// Validate mission-level agents
	for _, agentRef := range w.Agents {
		if !agentNames[agentRef] {
			return fmt.Errorf("agent '%s' not found", agentRef)
		}
	}

	// Validate folder references
	folderNames := make(map[string]bool)
	for _, sf := range sharedFolders {
		folderNames[sf.Name] = true
	}
	for _, folderRef := range w.Folders {
		if !folderNames[folderRef] {
			return fmt.Errorf("shared folder '%s' not found", folderRef)
		}
	}

	// Validate dedicated folder if present
	if w.Folder != nil {
		if err := w.Folder.Validate(); err != nil {
			return fmt.Errorf("folder: %w", err)
		}
	}

	// Validate run folder if present
	if w.RunFolder != nil {
		if err := w.RunFolder.Validate(); err != nil {
			return fmt.Errorf("run_folder: %w", err)
		}
	}

	// Validate each task
	for _, t := range w.Tasks {
		if err := t.Validate(taskNames, agentNames, datasetNames, w.Agents, allMissionNames); err != nil {
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

	// Validate schedules
	for i, sched := range w.Schedules {
		if err := sched.Validate(); err != nil {
			return fmt.Errorf("schedule[%d]: %w", i, err)
		}
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
	}
	if !validTypes[i.Type] {
		return fmt.Errorf("invalid type '%s': must be string, number, integer, bool, list, or object", i.Type)
	}

	// Integer defaults must be whole numbers
	if i.Type == InputTypeInteger && i.Default != nil {
		bf := i.Default.AsBigFloat()
		if !bf.IsInt() {
			return fmt.Errorf("input %q: default value must be a whole number for integer type", i.Name)
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
	case InputTypeInteger:
		f, err := strconv.ParseFloat(strVal, 64)
		if err != nil {
			return cty.NilVal, fmt.Errorf("invalid integer: %w", err)
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
// missionAgents are the agents defined at the mission level
func (t *Task) Validate(taskNames map[string]bool, agentNames map[string]bool, datasetNames map[string]bool, missionAgents []string, allMissionNames map[string]bool) error {
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
