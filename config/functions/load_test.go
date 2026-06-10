package functions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// call invokes the load function with the given configDir and rawPath and
// returns the result string and error. Keeps individual test bodies focused on
// behavior rather than cty plumbing.
func call(t *testing.T, configDir, rawPath string) (string, error) {
	t.Helper()
	fn := MakeLoadFunc(configDir)
	out, err := fn.Call([]cty.Value{cty.StringVal(rawPath)})
	if err != nil {
		return "", err
	}
	return out.AsString(), nil
}

func TestLoad_RelativePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "adjacent.md"), []byte("adjacent content"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got, err := call(t, dir, "./adjacent.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "adjacent content" {
		t.Errorf("got %q, want %q", got, "adjacent content")
	}
}

// TestLoad_BarePathFromConfigDir verifies that paths without a `./` prefix
// resolve against configDir (the project root) rather than the process
// working directory. This is the post-CWD-removal contract: CWD must never
// affect file resolution.
func TestLoad_BarePathFromConfigDir(t *testing.T) {
	// Chdir to a completely different tempdir to prove CWD doesn't matter.
	origCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	otherCWD := t.TempDir()
	if err := os.Chdir(otherCWD); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCWD) })

	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "guide.md"), []byte("from configDir"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := call(t, configDir, "guide.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from configDir" {
		t.Errorf("got %q, want %q", got, "from configDir")
	}
}

// TestLoad_ProjectRootMarker exercises the "@/foo" syntax that explicitly
// anchors a path to the project root (configDir).
func TestLoad_ProjectRootMarker(t *testing.T) {
	configDir := t.TempDir()
	sub := filepath.Join(configDir, "nested")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "deep.md"), []byte("deep"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := call(t, configDir, "@/nested/deep.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "deep" {
		t.Errorf("got %q, want %q", got, "deep")
	}
}

func TestLoad_RejectsAbsolutePath(t *testing.T) {
	// Use a .md path so this exercises the absolute-path rejection rather
	// than tripping over the extension check first.
	_, err := call(t, t.TempDir(), "/etc/hosts.md")
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
	if !strings.Contains(err.Error(), "absolute paths are not allowed") {
		t.Errorf("error %q missing expected message", err)
	}
}

// TestLoad_RejectsDotDotEscape ensures `..` traversal can't reach outside
// the project root even via a sub-config that uses ../ to reference its
// own parent.
func TestLoad_RejectsDotDotEscape(t *testing.T) {
	// configDir is the project root. A `..` from it should land outside.
	_, err := call(t, t.TempDir(), "../escaped.md")
	if err == nil {
		t.Fatal("expected error for path that escapes project root")
	}
	if !strings.Contains(err.Error(), "escapes the project root") {
		t.Errorf("error %q missing expected message", err)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := call(t, t.TempDir(), "./no_such_file.md")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "no_such_file.md") {
		t.Errorf("error %q should reference the missing filename", err)
	}
}

func TestLoad_AllowedExtensions(t *testing.T) {
	cases := map[string]string{
		"notes.md":  "markdown body",
		"notes.txt": "text body",
	}
	for filename, content := range cases {
		t.Run(filename, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got, err := call(t, dir, "./"+filename)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != content {
				t.Errorf("got %q, want %q", got, content)
			}
		})
	}
}

// TestLoad_RejectsDisallowedExtensions verifies the extension check happens
// before any filesystem access — the test passes a nonexistent file and still
// expects the extension error, not a "file not found" error.
func TestLoad_RejectsDisallowedExtensions(t *testing.T) {
	cases := []string{
		"data.json",
		"data.yaml",
		"data.hcl",
		"README",
	}
	for _, filename := range cases {
		t.Run(filename, func(t *testing.T) {
			_, err := call(t, t.TempDir(), "./"+filename)
			if err == nil {
				t.Fatal("expected error for disallowed extension")
			}
			if !strings.Contains(err.Error(), "only .md and .txt files are supported") {
				t.Errorf("error %q missing expected message", err)
			}
		})
	}
}
