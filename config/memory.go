package config

import "fmt"

// Reserved slot names for mission-scoped memory. Tool calls reference these
// via the `folder` parameter (e.g. `folder: "mission"` or `folder: "run"`).
const (
	PersistentSlotName = "mission"
	EphemeralSlotName  = "run"
)

// Valid values for the `type` attribute on a mission-scoped `memory` block.
const (
	MemoryTypePersistent = "persistent"
	MemoryTypeEphemeral  = "ephemeral"
)

// DefaultEphemeralCleanupDays is the auto-delete window applied to an
// ephemeral mission memory when `cleanup` is not set.
const DefaultEphemeralCleanupDays = 7

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

// Validate enforces naming rules. The literal names "mission" and "run" are
// reserved for the mission-scoped memory slots and must not be reused by a
// shared memory.
func (m *Memory) Validate() error {
	if m.Name == PersistentSlotName || m.Name == EphemeralSlotName {
		return fmt.Errorf("name %q is reserved for mission-scoped memory", m.Name)
	}
	return nil
}

// MissionMemory describes a `memory { ... }` block inside a mission. A
// mission may declare at most one persistent and one ephemeral memory.
//
// The storage path is derived by the runtime from the mission name and (for
// ephemeral) the mission instance ID, so no `path` is accepted from HCL.
//
// Cleanup is a pointer so we can distinguish "user did not set it" (apply
// default of 7 days, only meaningful for ephemeral) from "user set 0" (keep
// forever).
type MissionMemory struct {
	Type        string `hcl:"type,optional"`        // "persistent" (default) or "ephemeral"
	Description string `hcl:"description,optional"`
	Cleanup     *int   `hcl:"cleanup,optional"` // ephemeral only; days before auto-delete, 0 = never
}

// Validate normalizes Type (defaulting to persistent), enforces the cleanup
// rules, and rejects unknown type values.
func (mm *MissionMemory) Validate() error {
	if mm.Type == "" {
		mm.Type = MemoryTypePersistent
	}
	if mm.Type != MemoryTypePersistent && mm.Type != MemoryTypeEphemeral {
		return fmt.Errorf("type must be %q or %q (got %q)", MemoryTypePersistent, MemoryTypeEphemeral, mm.Type)
	}
	if mm.Type == MemoryTypePersistent && mm.Cleanup != nil {
		return fmt.Errorf("cleanup is only valid on ephemeral memory")
	}
	if mm.Cleanup != nil && *mm.Cleanup < 0 {
		return fmt.Errorf("cleanup must be >= 0 (days)")
	}
	return nil
}
