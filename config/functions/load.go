package functions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// MakeLoadFunc creates a load() HCL function that reads file contents as a string.
//
//   - load("./foo.md")  — resolves relative to configDir (the HCL file's directory)
//   - load("foo.md")    — resolves relative to the process working directory
//   - Paths starting with "/" are rejected (no filesystem root access)
//   - Only .md and .txt files are allowed
func MakeLoadFunc(configDir string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "path", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			rawPath := args[0].AsString()

			if strings.HasPrefix(rawPath, "/") {
				return cty.NilVal, fmt.Errorf("load(%q): absolute paths starting with '/' are not allowed", rawPath)
			}

			ext := strings.ToLower(filepath.Ext(rawPath))
			if ext != ".md" && ext != ".txt" {
				return cty.NilVal, fmt.Errorf("load(%q): only .md and .txt files are supported", rawPath)
			}

			var resolved string
			if strings.HasPrefix(rawPath, "./") || strings.HasPrefix(rawPath, "../") {
				resolved = filepath.Join(configDir, rawPath)
			} else {
				cwd, err := os.Getwd()
				if err != nil {
					return cty.NilVal, fmt.Errorf("load(%q): failed to get working directory: %w", rawPath, err)
				}
				resolved = filepath.Join(cwd, rawPath)
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return cty.NilVal, fmt.Errorf("load(%q): %w", rawPath, err)
			}

			return cty.StringVal(string(data)), nil
		},
	})
}
