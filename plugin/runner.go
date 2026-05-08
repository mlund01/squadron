package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const runnerFileName = "runner.json"

type Runner struct {
	Kind  string   `json:"kind"`
	Entry string   `json:"entry"`
	Args  []string `json:"args,omitempty"`
}

func writeRunner(dir string, r *Runner) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, runnerFileName), raw, 0644)
}

func readRunner(dir string) (*Runner, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, runnerFileName))
	if err != nil {
		return nil, false
	}
	var r Runner
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, false
	}
	if r.Entry == "" {
		return nil, false
	}
	return &r, true
}

func resolvePluginCommand(pluginDir string) (*exec.Cmd, error) {
	if r, ok := readRunner(pluginDir); ok {
		entry := r.Entry
		if !filepath.IsAbs(entry) {
			entry = filepath.Join(pluginDir, entry)
		}
		if _, err := os.Stat(entry); err != nil {
			return nil, fmt.Errorf("plugin entry %q (from runner.json) does not exist: %w", entry, err)
		}
		return exec.Command(entry, r.Args...), nil
	}

	binaryName := "plugin"
	if runtime.GOOS == "windows" {
		binaryName = "plugin.exe"
	}
	legacy := filepath.Join(pluginDir, binaryName)
	if _, err := os.Stat(legacy); err != nil {
		return nil, fmt.Errorf("no runner.json or %s binary found in %s", binaryName, pluginDir)
	}
	return exec.Command(legacy), nil
}
