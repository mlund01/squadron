package plugin

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// BuildLocal compiles a Go plugin from absSourcePath into the cache slot
// for (name, version). The source path must already be absolute and
// project-root-contained — path containment is the caller's responsibility
// (config-load and the CLI both go through paths.ResolveProjectPath).
func BuildLocal(name, version, absSourcePath string) error {
	pluginDir, err := GetPluginDir(name, version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return fmt.Errorf("create plugin directory: %w", err)
	}

	binaryName := "plugin"
	if runtime.GOOS == "windows" {
		binaryName = "plugin.exe"
	}
	outputPath := filepath.Join(pluginDir, binaryName)

	cmd := exec.Command("go", "build", "-o", outputPath, absSourcePath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build: %w\n%s", err, stderr.String())
	}
	return nil
}
