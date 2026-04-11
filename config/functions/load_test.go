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

func TestLoad_ParentRelativePath(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "workflows")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "shared.md"), []byte("shared content"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := call(t, sub, "../shared.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "shared content" {
		t.Errorf("got %q, want %q", got, "shared content")
	}
}

// TestLoad_BarePathFromCWD exercises the branch where paths without a ./ or ../
// prefix resolve against the process working directory. Kept as an integration
// test because os.Getwd() is a process-global call we can't inject around.
func TestLoad_BarePathFromCWD(t *testing.T) {
	origCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cwd := t.TempDir()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCWD) })

	if err := os.WriteFile(filepath.Join(cwd, "guide.md"), []byte("from cwd"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// configDir is a different temp dir to prove the bare path doesn't
	// resolve against it.
	configDir := t.TempDir()
	got, err := call(t, configDir, "guide.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from cwd" {
		t.Errorf("got %q, want %q", got, "from cwd")
	}
}

func TestLoad_RejectsAbsolutePath(t *testing.T) {
	_, err := call(t, t.TempDir(), "/etc/hosts")
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
	if !strings.Contains(err.Error(), "absolute paths starting with '/' are not allowed") {
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
