package plugin

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func installPython(pluginDir, source, scriptName string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("Python plugins on Windows are not yet supported")
	}

	pythonBin, err := findPython()
	if err != nil {
		return err
	}

	venvDir := filepath.Join(pluginDir, "venv")
	venvBin := filepath.Join(venvDir, "bin")

	fmt.Printf("  Creating venv (%s)...\n", pythonBin)
	if err := runStreamed(pythonBin, "-m", "venv", venvDir); err != nil {
		return fmt.Errorf("python venv creation failed: %w", err)
	}

	pip := filepath.Join(venvBin, "pip")
	fmt.Println("  Installing source...")
	if err := runStreamed(pip, "install", "--upgrade", "pip"); err != nil {
		return fmt.Errorf("pip upgrade failed: %w", err)
	}
	if err := runStreamed(pip, "install", source); err != nil {
		return fmt.Errorf("pip install failed: %w", err)
	}

	scriptPath := filepath.Join(venvBin, scriptName)
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("expected script %q not found at %s after install", scriptName, scriptPath)
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

func readWheelConsoleScripts(wheelPath string) (map[string]string, error) {
	r, err := zip.OpenReader(wheelPath)
	if err != nil {
		return nil, fmt.Errorf("open wheel: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if !strings.HasSuffix(f.Name, ".dist-info/entry_points.txt") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("read wheel entry_points.txt: %w", err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		return parseConsoleScripts(content), nil
	}
	return map[string]string{}, nil
}

func parseConsoleScripts(content []byte) map[string]string {
	scripts := map[string]string{}
	inSection := false
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inSection = line == "[console_scripts]"
			continue
		}
		if !inSection {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		scripts[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return scripts
}

func wheelScriptName(wheelPath string) (string, error) {
	scripts, err := readWheelConsoleScripts(wheelPath)
	if err != nil {
		return "", err
	}
	switch len(scripts) {
	case 0:
		return "", fmt.Errorf("wheel %s has no [console_scripts] entry", filepath.Base(wheelPath))
	case 1:
		for name := range scripts {
			return name, nil
		}
	default:
		var names []string
		for n := range scripts {
			names = append(names, n)
		}
		return "", fmt.Errorf("wheel %s has multiple [console_scripts] entries (%v) — squadron plugins must declare exactly one", filepath.Base(wheelPath), names)
	}
	return "", nil
}
