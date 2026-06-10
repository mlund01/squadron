package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// PacketSlotPrefix is the namespace prefix under which packets are
// registered in the MemoryStore. Tools address packets as
// "packet.<name>" via the `slot` parameter on every file tool.
//
// Mirrored in aitools.PacketSlotPrefix; both must stay in sync.
const PacketSlotPrefix = "packet."

// Packet is a read-only reference data bundle attached to missions.
// Unlike memory slots, packets:
//   - point at a user-controlled path (memory paths are derived by Squadron)
//   - are immutable to agents (no file_create / file_delete)
//   - reject binary content on read (text files only)
//   - have their folder tree excluded from HCL config parsing
//
// Packets surface under the `slot` parameter on every file tool as
// "packet.<name>" so they cannot collide with memory slot names. The
// `path` attribute is resolved by paths.ResolveConfigPath — absolute
// paths are rejected, "./foo" and bare "foo" anchor to the HCL file's
// directory, "@/foo" anchors to the project root, and any ".." traversal
// that escapes the project root is rejected at parse time.
type Packet struct {
	Name        string `hcl:"name,label"`
	Path        string `hcl:"path"`
	Description string `hcl:"description,optional"`
}

// Validate checks the name + that the (already-resolved-absolute) path
// exists and is a directory. The loader is responsible for calling
// paths.ResolveConfigPath before Validate so p.Path is absolute by the
// time we get here. Idempotent — safe to call repeatedly.
func (p *Packet) Validate() error {
	// Reuse the same naming rules as shared memory slots so packet names
	// can't accidentally collide with reserved slot names or break the
	// filesystem layout.
	if p.Name == MemorySlotName || p.Name == ScratchpadSlotName {
		return fmt.Errorf("name %q is reserved for mission-scoped slots", p.Name)
	}
	if err := validateSlotName(p.Name); err != nil {
		return err
	}
	if p.Path == "" {
		return fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(p.Path) {
		// Caller skipped paths.ResolveConfigPath — surface it clearly
		// rather than silently calling filepath.Abs and binding to
		// whatever the process CWD happens to be.
		return fmt.Errorf("path %q must be resolved to absolute before Validate (loader bug)", p.Path)
	}
	info, err := os.Stat(p.Path)
	if err != nil {
		return fmt.Errorf("path %q: %w", p.Path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path %q is not a directory", p.Path)
	}
	return nil
}
