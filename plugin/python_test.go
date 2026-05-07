package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConsoleScripts(t *testing.T) {
	content := []byte(`# headers ignored
[some_other_section]
foo = bar:baz

[console_scripts]
myplug = myplug.main:main
# comment
helper = pkg.cli:run

[gui_scripts]
gui = myplug.gui:main
`)
	got := parseConsoleScripts(content)
	want := map[string]string{
		"myplug": "myplug.main:main",
		"helper": "pkg.cli:run",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestWheelScriptName(t *testing.T) {
	wheel := "/tmp/wheel_out/myplug-0.1.0-py3-none-any.whl"
	if _, err := os.Stat(wheel); err != nil {
		t.Skipf("wheel not present at %s — skipping", wheel)
	}
	name, err := wheelScriptName(wheel)
	if err != nil {
		t.Fatalf("wheelScriptName: %v", err)
	}
	if name != "myplug" {
		t.Fatalf("got %q, want myplug", name)
	}
}

func TestInstallPythonFromWheel(t *testing.T) {
	wheel := "/tmp/wheel_out/myplug-0.1.0-py3-none-any.whl"
	if _, err := os.Stat(wheel); err != nil {
		t.Skipf("wheel not present at %s — skipping", wheel)
	}
	destDir := t.TempDir()
	if err := installPython(destDir, wheel, "myplug"); err != nil {
		t.Fatalf("installPython: %v", err)
	}

	runner, ok := readRunner(destDir)
	if !ok {
		t.Fatal("runner.json not written")
	}
	if runner.Kind != "python" {
		t.Errorf("kind = %q, want python", runner.Kind)
	}
	if runner.Entry != filepath.Join("venv", "bin", "myplug") {
		t.Errorf("entry = %q, want venv/bin/myplug", runner.Entry)
	}

	scriptPath := filepath.Join(destDir, runner.Entry)
	if _, err := os.Stat(scriptPath); err != nil {
		t.Errorf("script %s not present after install: %v", scriptPath, err)
	}
}
