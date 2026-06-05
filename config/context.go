package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ContextSlotPrefix is the namespace prefix under which contexts are
// registered in the MemoryStore. Tools address contexts as
// "context.<name>" via the `slot` parameter on every file tool.
//
// Mirrored in aitools.ContextSlotPrefix; both must stay in sync.
const ContextSlotPrefix = "context."

// Context is a read-only reference data bundle attached to missions.
// Unlike memory slots, contexts:
//   - point at a user-controlled path (memory paths are derived by Squadron)
//   - are immutable to agents (no file_create / file_delete)
//   - reject binary content on read (text files only)
//   - have their folder tree excluded from HCL config parsing
//
// Contexts surface under the `slot` parameter on every file tool as
// "context.<name>" so they cannot collide with memory slot names. The
// `path` attribute is resolved by paths.ResolveConfigPath — absolute
// paths are rejected, "./foo" and bare "foo" anchor to the HCL file's
// directory, "@/foo" anchors to the project root, and any "..` traversal
// that escapes the project root is rejected at parse time.
type Context struct {
	Name        string `hcl:"name,label"`
	Path        string `hcl:"path"`
	Description string `hcl:"description,optional"`
}

// Validate checks the name + that the (already-resolved-absolute) path
// exists and is a directory. The loader is responsible for calling
// paths.ResolveConfigPath before Validate so c.Path is absolute by the
// time we get here. Idempotent — safe to call repeatedly.
func (c *Context) Validate() error {
	// Reuse the same naming rules as shared memory slots so context names
	// can't accidentally collide with reserved slot names or break the
	// filesystem layout.
	if c.Name == MemorySlotName || c.Name == ScratchpadSlotName {
		return fmt.Errorf("name %q is reserved for mission-scoped slots", c.Name)
	}
	if err := validateSlotName(c.Name); err != nil {
		return err
	}
	if c.Path == "" {
		return fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(c.Path) {
		// Caller skipped paths.ResolveConfigPath — surface it clearly
		// rather than silently calling filepath.Abs and binding to
		// whatever the process CWD happens to be.
		return fmt.Errorf("path %q must be resolved to absolute before Validate (loader bug)", c.Path)
	}
	info, err := os.Stat(c.Path)
	if err != nil {
		return fmt.Errorf("path %q: %w", c.Path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path %q is not a directory", c.Path)
	}
	return nil
}
