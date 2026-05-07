package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunnerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &Runner{Kind: "python", Entry: "venv/bin/myplug", Args: []string{"--debug"}}
	if err := writeRunner(dir, want); err != nil {
		t.Fatalf("writeRunner: %v", err)
	}
	got, ok := readRunner(dir)
	if !ok {
		t.Fatal("readRunner: not found")
	}
	if got.Kind != want.Kind || got.Entry != want.Entry || len(got.Args) != 1 || got.Args[0] != "--debug" {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestReadRunnerMissing(t *testing.T) {
	if _, ok := readRunner(t.TempDir()); ok {
		t.Fatal("expected missing runner.json to return ok=false")
	}
}

func TestReadRunnerRejectsEmptyEntry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, runnerFileName), []byte(`{"kind":"python","entry":""}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readRunner(dir); ok {
		t.Fatal("expected runner with empty entry to be rejected")
	}
}

func TestResolvePluginCommandFromRunner(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "myscript")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeRunner(dir, &Runner{Kind: "python", Entry: "myscript", Args: []string{"-x"}}); err != nil {
		t.Fatal(err)
	}
	cmd, err := resolvePluginCommand(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cmd.Path != entry {
		t.Errorf("Path = %q, want %q", cmd.Path, entry)
	}
	if len(cmd.Args) < 2 || cmd.Args[1] != "-x" {
		t.Errorf("Args = %v, want [..., -x]", cmd.Args)
	}
}

func TestResolvePluginCommandFallsBackToBinary(t *testing.T) {
	dir := t.TempDir()
	binaryName := "plugin"
	if runtime.GOOS == "windows" {
		binaryName = "plugin.exe"
	}
	binary := filepath.Join(dir, binaryName)
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd, err := resolvePluginCommand(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cmd.Path != binary {
		t.Errorf("expected fallback to binary %q, got %q", binary, cmd.Path)
	}
}

func TestResolvePluginCommandErrorsWhenNothingPresent(t *testing.T) {
	if _, err := resolvePluginCommand(t.TempDir()); err == nil {
		t.Fatal("expected error when no runner.json or plugin binary is present")
	}
}

func TestResolvePluginCommandErrorsWhenRunnerEntryMissing(t *testing.T) {
	dir := t.TempDir()
	if err := writeRunner(dir, &Runner{Kind: "python", Entry: "venv/bin/missing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePluginCommand(dir); err == nil {
		t.Fatal("expected error when runner.json points at a non-existent entry")
	}
}
