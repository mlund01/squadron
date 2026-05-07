package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePyproject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "pyproject.toml")
}

func TestReadPyprojectScript_Single(t *testing.T) {
	path := writePyproject(t, `
[project]
name = "myplug"
version = "0.1.0"

[project.scripts]
myplug = "myplug.main:main"
`)
	got, err := readPyprojectScript(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "myplug" {
		t.Errorf("got %q, want myplug", got)
	}
}

func TestReadPyprojectScript_NoScripts(t *testing.T) {
	path := writePyproject(t, `
[project]
name = "myplug"
version = "0.1.0"
`)
	_, err := readPyprojectScript(path)
	if err == nil || !strings.Contains(err.Error(), "no [project.scripts]") {
		t.Fatalf("expected no-scripts error, got %v", err)
	}
}

func TestReadPyprojectScript_MultipleRejected(t *testing.T) {
	path := writePyproject(t, `
[project]
name = "myplug"
version = "0.1.0"

[project.scripts]
foo = "myplug.main:foo"
bar = "myplug.main:bar"
`)
	_, err := readPyprojectScript(path)
	if err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected multiple-scripts error, got %v", err)
	}
}

func TestReadPyprojectScript_Malformed(t *testing.T) {
	path := writePyproject(t, "not [valid toml")
	_, err := readPyprojectScript(path)
	if err == nil {
		t.Fatal("expected parse error on malformed toml")
	}
}

func TestReadPyprojectScript_FileMissing(t *testing.T) {
	_, err := readPyprojectScript(filepath.Join(t.TempDir(), "nope.toml"))
	if err == nil {
		t.Fatal("expected error on missing file")
	}
}
