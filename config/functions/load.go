package functions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"

	"squadron/internal/paths"
)

// MakeLoadFunc creates a load() HCL function that reads file contents as a
// string. Paths follow the project-wide resolution rule (see
// paths.ResolveConfigPath):
//
//   - "@/foo.md"       — resolves relative to the project root (configDir)
//   - "./foo.md"       — resolves relative to the project root (configDir);
//     load() has no per-callsite "HCL file" context, so `.` collapses to
//     the project root
//   - "foo.md"  (bare) — same as "./foo.md"
//   - "/foo.md"        — REJECTED (no filesystem-root access)
//   - ".." traversal that escapes the project root is also rejected
//
// Only .md and .txt files are allowed.
func MakeLoadFunc(configDir string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "path", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			rawPath := args[0].AsString()

			ext := strings.ToLower(filepath.Ext(rawPath))
			if ext != ".md" && ext != ".txt" {
				return cty.NilVal, fmt.Errorf("load(%q): only .md and .txt files are supported", rawPath)
			}

			// load() doesn't have a per-callsite "this HCL file" — every
			// call resolves against the project root. Pass configDir as
			// both projectRoot and hclFileDir so bare/`./` names anchor
			// there too. Absolute paths and `..` escapes are rejected.
			resolved, err := paths.ResolveConfigPath(configDir, configDir, rawPath)
			if err != nil {
				return cty.NilVal, fmt.Errorf("load(%q): %w", rawPath, err)
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return cty.NilVal, fmt.Errorf("load(%q): %w", rawPath, err)
			}

			return cty.StringVal(string(data)), nil
		},
	})
}
