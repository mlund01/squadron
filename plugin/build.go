package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/pelletier/go-toml/v2"
)

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
	if runtime.GOOS == "windows" {
		return fmt.Errorf("Python plugin builds on Windows are not yet supported")
	}

	scriptName, err := readPyprojectScript(filepath.Join(sourcePath, "pyproject.toml"))
	if err != nil {
		return err
	}

	pythonBin, err := findPython()
	if err != nil {
		return err
	}

	venvDir := filepath.Join(pluginDir, "venv")
	venvBin := filepath.Join(venvDir, "bin")

	fmt.Printf("  Output: %s\n", venvDir)
	fmt.Printf("  Creating venv (%s)...\n", pythonBin)
	if err := runStreamed(pythonBin, "-m", "venv", venvDir); err != nil {
		return fmt.Errorf("python venv creation failed: %w", err)
	}

	pip := filepath.Join(venvBin, "pip")
	fmt.Println("  Installing source...")
	if err := runStreamed(pip, "install", "--upgrade", "pip"); err != nil {
		return fmt.Errorf("pip upgrade failed: %w", err)
	}
	if err := runStreamed(pip, "install", sourcePath); err != nil {
		return fmt.Errorf("pip install failed: %w", err)
	}

	scriptPath := filepath.Join(venvBin, scriptName)
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("expected script %q from [project.scripts] not found at %s after install", scriptName, scriptPath)
	}

	runner := &Runner{
		Kind:  "python",
		Entry: filepath.Join("venv", "bin", scriptName),
	}
	if err := writeRunner(pluginDir, runner); err != nil {
		return fmt.Errorf("write runner.json: %w", err)
	}

	fmt.Printf("  Entry: %s\n", runner.Entry)
	return nil
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

