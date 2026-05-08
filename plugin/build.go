package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/pelletier/go-toml/v2"
)

// BuildLocal compiles a local plugin source into the cache slot for
// (name, version) and writes runner.json. Detects Go (go.mod) vs Python
// (pyproject.toml) and dispatches to BuildGo / BuildPython.
//
// The source path must already be absolute and project-root-contained —
// path containment is the caller's responsibility (config-load and the
// CLI both go through paths.ResolveProjectPath).
func BuildLocal(name, version, absSourcePath string) error {
	pluginDir, err := GetPluginDir(name, version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return fmt.Errorf("create plugin directory: %w", err)
	}

	switch {
	case fileExists(filepath.Join(absSourcePath, "go.mod")):
		return BuildGo(pluginDir, absSourcePath)
	case fileExists(filepath.Join(absSourcePath, "pyproject.toml")):
		return BuildPython(pluginDir, absSourcePath)
	default:
		return fmt.Errorf("source %q has no go.mod or pyproject.toml — can't determine plugin language", absSourcePath)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func BuildGo(pluginDir, sourcePath string) error {
	binaryName := "plugin"
	if runtime.GOOS == "windows" {
		binaryName = "plugin.exe"
	}
	output := filepath.Join(pluginDir, binaryName)

	fmt.Printf("  Output: %s\n", output)
	cmd := exec.Command("go", "build", "-o", output, ".")
	cmd.Dir = sourcePath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}

	if err := writeRunner(pluginDir, &Runner{Kind: "go", Entry: binaryName}); err != nil {
		return fmt.Errorf("write runner.json: %w", err)
	}
	return nil
}

func BuildPython(pluginDir, sourcePath string) error {
	scriptName, err := readPyprojectScript(filepath.Join(sourcePath, "pyproject.toml"))
	if err != nil {
		return err
	}
	fmt.Printf("  Output: %s\n", filepath.Join(pluginDir, "venv"))
	return installPython(pluginDir, sourcePath, scriptName)
}

func readPyprojectScript(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read pyproject.toml: %w", err)
	}
	var doc struct {
		Project struct {
			Scripts map[string]string `toml:"scripts"`
		} `toml:"project"`
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse pyproject.toml: %w", err)
	}
	scripts := doc.Project.Scripts
	switch len(scripts) {
	case 0:
		return "", fmt.Errorf("pyproject.toml has no [project.scripts] entry — declare exactly one to mark the plugin entry point")
	case 1:
		for name := range scripts {
			return name, nil
		}
	default:
		var names []string
		for n := range scripts {
			names = append(names, n)
		}
		return "", fmt.Errorf("pyproject.toml has multiple [project.scripts] entries (%v) — squadron plugins must declare exactly one", names)
	}
	return "", nil
}

func findPython() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("python3 not found on PATH")
}

func runStreamed(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
