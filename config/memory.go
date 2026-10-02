package config

import (
	"fmt"
	"strings"
)

// Reserved slot names for the mission-scoped storage slots. Agents address
// them via the `slot` parameter on the file tools, alongside any shared
// memory names declared at the top level.
const (
	MemorySlotName     = "memory"
	ScratchpadSlotName = "scratchpad"
)

// ScratchpadCleanupDays is the auto-delete window applied to every
// mission's scratchpad. Not user-configurable — scratchpads are deliberately
// short-lived.
const ScratchpadCleanupDays = 7

// Memory describes a top-level shared memory block:
//
//	memory "research" {
//	  description = "..."
//	}
//
// The storage path is derived by the runtime — it lives at
// `<squadron_home>/memories/shared/<name>/`. Description is required so
// agents can tell what each shared slot is for. All memories are writable
// by their agents.
type Memory struct {
	Name        string `hcl:"name,label"`
	Description string `hcl:"description"`
}

// Validate enforces naming rules + the required description.
func (m *Memory) Validate() error {
	if m.Name == MemorySlotName || m.Name == ScratchpadSlotName {
		return fmt.Errorf("name %q is reserved for mission-scoped slots", m.Name)
	}
	if err := validateSlotName(m.Name); err != nil {
		return err
	}
	if m.Description == "" {
		return fmt.Errorf("description is required")
	}
	return nil
}

// validateSlotName rejects HCL labels that would break filesystem layout:
// path separators, parent-dir traversal, or leading dot. Used by both shared
// memory labels and mission names (since both become directory names under
// <squadron_home>).
func validateSlotName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("name %q must not contain path separators", name)
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return fmt.Errorf("name %q must not start with '.'", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("name %q must not contain '..'", name)
	}
	return nil
}

// MissionMemory describes the `memory { ... }` block inside a mission —
// persistent storage that survives across runs. At most one per mission.
//
// The storage path is derived by the runtime from the mission name, so no
// `path` is accepted from HCL.
type MissionMemory struct {
	Description   string               `hcl:"description" json:"description"`
	RetentionDays int                  `hcl:"retention_days,optional" json:"retentionDays,omitempty"`
	ReadTasks     []string             `hcl:"read_tasks,optional" json:"readTasks,omitempty"`
	WriteTasks    []string             `hcl:"write_tasks,optional" json:"writeTasks,omitempty"`
	Fields        []MissionMemoryField `hcl:"field,block" json:"fields,omitempty"`
}

// MissionMemoryField defines one top-level JSON key that memory may hold.
// Object and array fields are type-checked but have no nested schema yet.
type MissionMemoryField struct {
	Name string `hcl:"name,label" json:"name"`
	Type string `hcl:"type" json:"type"`
}

func (mm *MissionMemory) HasPolicy() bool {
	return mm.RetentionDays != 0 || len(mm.ReadTasks) > 0 || len(mm.WriteTasks) > 0 || len(mm.Fields) > 0
}

// Validate enforces the required description.
func (mm *MissionMemory) Validate() error {
	if mm.Description == "" {
		return fmt.Errorf("description is required")
	}
	if !mm.HasPolicy() {
		return nil // Legacy declaration remains inert; the runtime refuses to run it.
	}
	if mm.RetentionDays < 1 || mm.RetentionDays > 3650 {
		return fmt.Errorf("retention_days must be between 1 and 3650")
	}
	if len(mm.ReadTasks) == 0 || len(mm.Fields) == 0 {
		return fmt.Errorf("read_tasks and at least one field are required")
	}
	readers := make(map[string]bool, len(mm.ReadTasks))
	for _, task := range mm.ReadTasks {
		if task == "" || readers[task] {
			return fmt.Errorf("read_tasks must contain unique, nonempty names")
		}
		readers[task] = true
	}
	writers := make(map[string]bool, len(mm.WriteTasks))
	for _, task := range mm.WriteTasks {
		if !readers[task] || writers[task] {
			return fmt.Errorf("write task %q must be a unique member of read_tasks", task)
		}
		writers[task] = true
	}
	fields := make(map[string]bool, len(mm.Fields))
	for _, field := range mm.Fields {
		if field.Name == "" || fields[field.Name] {
			return fmt.Errorf("memory fields must have unique, nonempty names")
		}
		fields[field.Name] = true
		switch field.Type {
		case "string", "number", "integer", "boolean", "object", "array":
		default:
			return fmt.Errorf("memory field %q has unsupported type %q", field.Name, field.Type)
		}
	}
	return nil
}

func (mm *MissionMemory) ValidateTasks(taskNames map[string]bool) error {
	for _, task := range append(append([]string(nil), mm.ReadTasks...), mm.WriteTasks...) {
		if !taskNames[task] {
			return fmt.Errorf("memory access references unknown task %q", task)
		}
	}
	return nil
}
