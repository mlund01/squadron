package config

import "fmt"

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
	if m.Description == "" {
		return fmt.Errorf("description is required")
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
