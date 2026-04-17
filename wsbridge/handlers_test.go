package wsbridge

import (
	"os"
	"path/filepath"
	"testing"
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
