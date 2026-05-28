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
// Same surface as the top-level Memory block minus the label: a required
// description.
//
// The storage path is derived by the runtime from the mission name, so no
// `path` is accepted from HCL.
type MissionMemory struct {
	Description string `hcl:"description"`
}

// Validate enforces the required description.
func (mm *MissionMemory) Validate() error {
	if mm.Description == "" {
		return fmt.Errorf("description is required")
	}
	return nil
}
