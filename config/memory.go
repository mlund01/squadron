package config

import "fmt"

// Reserved slot names for mission-scoped storage. Tool calls reference these
// via the `slot` parameter (e.g. `slot: "memory"` or `slot: "scratchpad"`).
const (
	MemorySlotName     = "memory"
	ScratchpadSlotName = "scratchpad"
)

// DefaultScratchpadCleanupDays is the auto-delete window applied to a
// mission's scratchpad when `cleanup` is not set.
const DefaultScratchpadCleanupDays = 7

// Memory describes a top-level shared memory block:
//
//	memory "research" {
//	  description = "..."
//	  label       = "..."
//	  editable    = true
//	}
//
// The storage path is derived by the runtime — it lives at
// `<squadron_home>/memories/shared/<name>/` — so no `path` is accepted from
// HCL.
type Memory struct {
	Name        string `hcl:"name,label"`
	Description string `hcl:"description,optional"`
	Label       string `hcl:"label,optional"`
	Editable    bool   `hcl:"editable,optional"`
}

// Validate enforces naming rules. The literal names "memory" and "scratchpad"
// are reserved for mission-scoped slots and must not be reused by a shared
// memory.
func (m *Memory) Validate() error {
	if m.Name == MemorySlotName || m.Name == ScratchpadSlotName {
		return fmt.Errorf("name %q is reserved for mission-scoped slots", m.Name)
	}
	return nil
}

// MissionMemory describes the `memory { ... }` block inside a mission —
// persistent storage that survives across runs. At most one per mission.
//
// Path is derived by the runtime from the mission name, so no `path` is
// accepted from HCL.
type MissionMemory struct {
	Description string `hcl:"description,optional"`
}

// Validate is a no-op today; kept so the type satisfies the same surface as
// MissionScratchpad and so future fields can grow into it.
func (mm *MissionMemory) Validate() error { return nil }

// MissionScratchpad describes the `scratchpad { ... }` block inside a
// mission — ephemeral per-run working space. At most one per mission. A
// fresh directory is materialized for each mission instance and the cleanup
// sweep deletes ones older than `cleanup` days.
//
// Path is derived by the runtime from the mission name and mission instance
// ID, so no `path` is accepted from HCL.
//
// Cleanup is a pointer so we can distinguish "user did not set it" (apply
// the default) from "user set 0" (keep forever).
type MissionScratchpad struct {
	Description string `hcl:"description,optional"`
	Cleanup     *int   `hcl:"cleanup,optional"` // days before auto-delete; 0 = never
}

// Validate rejects negative cleanup values.
func (ms *MissionScratchpad) Validate() error {
	if ms.Cleanup != nil && *ms.Cleanup < 0 {
		return fmt.Errorf("cleanup must be >= 0 (days)")
	}
	return nil
}
