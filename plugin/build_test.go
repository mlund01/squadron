package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"squadron/internal/paths"
)

// withSquadronHome points paths.SquadronHome at a fresh temp dir for the
// life of the test. paths.SquadronHome caches via sync.Once so t.Setenv
// alone isn't enough — ResetHome clears the cache before and after.
func withSquadronHome(t *testing.T) {
	t.Helper()
	paths.ResetHome()
	t.Setenv("SQUADRON_HOME", t.TempDir())
	t.Cleanup(paths.ResetHome)
}

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

// TestBuildLocal_UnknownLanguage exercises the dispatch in BuildLocal:
// a source dir with neither go.mod nor pyproject.toml must fail with a
// clear error, before any build tool is invoked.
func TestBuildLocal_UnknownLanguage(t *testing.T) {
	withSquadronHome(t)
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("not a plugin"), 0644); err != nil {
		t.Fatal(err)
	}
	err := BuildLocal("unknown_plug", "local", src)
	if err == nil {
		t.Fatal("expected error for source with no go.mod or pyproject.toml")
	}
	if !strings.Contains(err.Error(), "no go.mod or pyproject.toml") {
		t.Fatalf("expected language-detection error, got: %v", err)
	}
}

// TestBuildLocal_DispatchesToGo verifies that a source dir containing a
// go.mod is routed through BuildGo and produces an installed binary at
// the expected runner.json-anchored path. Uses a no-op `main` package so
// the test doesn't depend on the squadron-sdk being available.
func TestBuildLocal_DispatchesToGo(t *testing.T) {
	withSquadronHome(t)
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "go.mod"), "module localplug\n\ngo 1.21\n")
	mustWrite(t, filepath.Join(src, "main.go"), "package main\n\nfunc main() {}\n")

	if err := BuildLocal("localplug", "local", src); err != nil {
		t.Fatalf("BuildLocal failed: %v", err)
	}

	pluginDir, err := GetPluginDir("localplug", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, runnerFileName)); err != nil {
		t.Fatalf("runner.json not written: %v", err)
	}
	r, ok := readRunner(pluginDir)
	if !ok {
		t.Fatal("readRunner returned not-ok")
	}
	if r.Kind != "go" {
		t.Errorf("kind = %q, want go", r.Kind)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, r.Entry)); err != nil {
		t.Errorf("plugin binary not at runner.Entry %q: %v", r.Entry, err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestBuildLocal_SkipsWhenSourceUnchanged confirms the staleness gate:
// a second BuildLocal call against the same source tree must not
// re-invoke `go build`. We detect "didn't rebuild" by checking the
// binary's mtime — if the build runs, the binary is overwritten and
// mtime advances.
func TestBuildLocal_SkipsWhenSourceUnchanged(t *testing.T) {
	withSquadronHome(t)
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "go.mod"), "module localplug\n\ngo 1.21\n")
	mustWrite(t, filepath.Join(src, "main.go"), "package main\n\nfunc main() {}\n")

	if err := BuildLocal("cachable", "local", src); err != nil {
		t.Fatalf("first build: %v", err)
	}

	pluginDir, err := GetPluginDir("cachable", "local")
	if err != nil {
		t.Fatal(err)
	}
	r, ok := readRunner(pluginDir)
	if !ok {
		t.Fatal("runner.json missing after first build")
	}
	if r.SourceHash == "" {
		t.Fatal("SourceHash not stamped after first build")
	}
	first, err := os.Stat(filepath.Join(pluginDir, r.Entry))
	if err != nil {
		t.Fatal(err)
	}

	// Backdate the binary so any rebuild would advance its mtime.
	old := first.ModTime().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(pluginDir, r.Entry), old, old); err != nil {
		t.Fatal(err)
	}

	if err := BuildLocal("cachable", "local", src); err != nil {
		t.Fatalf("second build: %v", err)
	}
	second, err := os.Stat(filepath.Join(pluginDir, r.Entry))
	if err != nil {
		t.Fatal(err)
	}
	if !second.ModTime().Equal(old) {
		t.Errorf("binary was rebuilt despite unchanged source (mtime: %v → %v)", old, second.ModTime())
	}
}

// TestBuildLocal_RebuildsAfterEdit is the complement to the skip test:
// any edit must trigger a fresh build.
func TestBuildLocal_RebuildsAfterEdit(t *testing.T) {
	withSquadronHome(t)
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "go.mod"), "module localplug\n\ngo 1.21\n")
	mustWrite(t, filepath.Join(src, "main.go"), "package main\n\nfunc main() {}\n")

	if err := BuildLocal("editable", "local", src); err != nil {
		t.Fatalf("first build: %v", err)
	}

	pluginDir, err := GetPluginDir("editable", "local")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := readRunner(pluginDir)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(pluginDir, r.Entry), old, old); err != nil {
		t.Fatal(err)
	}

	// Edit the source — same byte length so it's an obvious content change.
	mustWrite(t, filepath.Join(src, "main.go"), "package main\n\nfunc main() {/*x*/}\n")

	if err := BuildLocal("editable", "local", src); err != nil {
		t.Fatalf("second build: %v", err)
	}
	st, err := os.Stat(filepath.Join(pluginDir, r.Entry))
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().After(old) {
		t.Errorf("binary mtime did not advance after source edit (still %v)", st.ModTime())
	}
}
