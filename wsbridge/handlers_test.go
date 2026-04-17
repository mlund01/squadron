package wsbridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"squadron/config"
)

// TestCopyConfigTree_IncludesLoadableFiles verifies the validation copy
// captures .md and .txt files alongside .hcl, so load() references resolve
// against the temp dir. See issue #47.
func TestCopyConfigTree_IncludesLoadableFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Arrange a realistic config tree with a nested skill file.
	writeFile(t, filepath.Join(src, "main.hcl"), `skill "x" {}`)
	writeFile(t, filepath.Join(src, "skills", "devin_code.md"), "# Skill instructions\n")
	writeFile(t, filepath.Join(src, "prompts", "system.txt"), "be terse")
	// Non-config files should not be copied.
	writeFile(t, filepath.Join(src, "secret.env"), "API_KEY=xxx")
	writeFile(t, filepath.Join(src, "image.png"), "binary")
	// Hidden directories should be skipped (e.g., .git, .squadron).
	writeFile(t, filepath.Join(src, ".squadron", "store.db"), "db")
	writeFile(t, filepath.Join(src, ".git", "HEAD"), "ref")

	copyConfigTree(src, dst)

	assertFileEquals(t, filepath.Join(dst, "main.hcl"), `skill "x" {}`)
	assertFileEquals(t, filepath.Join(dst, "skills", "devin_code.md"), "# Skill instructions\n")
	assertFileEquals(t, filepath.Join(dst, "prompts", "system.txt"), "be terse")

	assertMissing(t, filepath.Join(dst, "secret.env"))
	assertMissing(t, filepath.Join(dst, "image.png"))
	assertMissing(t, filepath.Join(dst, ".squadron", "store.db"))
	assertMissing(t, filepath.Join(dst, ".git", "HEAD"))
}

// TestCopyConfigTree_MissingSource doesn't panic on a non-existent source dir.
func TestCopyConfigTree_MissingSource(t *testing.T) {
	dst := t.TempDir()
	copyConfigTree(filepath.Join(t.TempDir(), "does-not-exist"), dst)
}

// TestCopyConfigTree_LoadFunctionResolvesCopiedFiles is the end-to-end regression
// guard for issue #47: after copying, config.LoadAndValidate must be able to
// resolve load("./x.md") and load("./x.txt") references against the temp dir.
func TestCopyConfigTree_LoadFunctionResolvesCopiedFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "config.hcl"), baseHCL+`
skill "md_skill" {
  description  = "from markdown"
  instructions = load("./skills/md_body.md")
}

skill "txt_skill" {
  description  = "from text"
  instructions = load("./skills/txt_body.txt")
}
`)
	writeFile(t, filepath.Join(src, "skills", "md_body.md"), "markdown-instructions")
	writeFile(t, filepath.Join(src, "skills", "txt_body.txt"), "text-instructions")

	copyConfigTree(src, dst)

	cfg, err := config.LoadAndValidate(dst)
	if err != nil {
		t.Fatalf("LoadAndValidate: %v", err)
	}

	got := map[string]string{}
	for _, s := range cfg.Skills {
		got[s.Name] = s.Instructions
	}
	if got["md_skill"] != "markdown-instructions" {
		t.Errorf("md_skill instructions = %q, want %q", got["md_skill"], "markdown-instructions")
	}
	if got["txt_skill"] != "text-instructions" {
		t.Errorf("txt_skill instructions = %q, want %q", got["txt_skill"], "text-instructions")
	}
}

// TestCopyConfigTree_LoadIgnoresUncopiedExtensions verifies the exclusivity:
// files with other extensions are not carried over, so load() references to
// them fail with "no such file or directory" against the temp dir — which is
// exactly the symptom issue #47 reported for .md/.txt before this fix.
func TestCopyConfigTree_LoadIgnoresUncopiedExtensions(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// A .json sibling that load() would reject at the extension check, plus an
	// .html sibling load() doesn't know about — neither should be copied.
	writeFile(t, filepath.Join(src, "config.hcl"), baseHCL)
	writeFile(t, filepath.Join(src, "data.json"), `{"x":1}`)
	writeFile(t, filepath.Join(src, "page.html"), "<html>")
	writeFile(t, filepath.Join(src, "keep.md"), "kept")

	copyConfigTree(src, dst)

	// Only .hcl and .md survive in the destination.
	entries := walkFiles(t, dst)
	for _, e := range entries {
		if !strings.HasSuffix(e, ".hcl") && !strings.HasSuffix(e, ".md") && !strings.HasSuffix(e, ".txt") {
			t.Errorf("unexpected file copied: %s", e)
		}
	}
	assertMissing(t, filepath.Join(dst, "data.json"))
	assertMissing(t, filepath.Join(dst, "page.html"))
	assertFileEquals(t, filepath.Join(dst, "keep.md"), "kept")
}

// baseHCL is the minimal HCL needed by config.LoadAndValidate (variable + storage
// block). Inlined here rather than shared with config's ginkgo suite because that
// lives in a package-external _test.go and isn't importable.
const baseHCL = `
variable "test_api_key" {
  default = "test-key-123"
}
storage {
  backend = "sqlite"
}
`

func walkFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(root, path)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertFileEquals(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s: got %q, want %q", path, string(got), want)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s: expected not to exist, stat err = %v", path, err)
	}
}
