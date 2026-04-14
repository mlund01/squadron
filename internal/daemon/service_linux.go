//go:build linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const unitName = "squadron.service"

const unitTemplate = `[Unit]
Description=Squadron Agent Framework
After=network.target

[Service]
Type=simple
ExecStart={{.Binary}} engage --foreground -c {{.ConfigPath}} --no-browser
WorkingDirectory={{.WorkDir}}
Restart=always
RestartSec=5
StandardOutput=append:{{.LogPath}}
StandardError=append:{{.LogPath}}

[Install]
WantedBy=default.target
`

type unitData struct {
	Binary     string
	ConfigPath string
	LogPath    string
	WorkDir    string
}

func unitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", unitName)
}

// InstallService generates a systemd user unit and enables it.
func InstallService(configPath string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine executable path: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("could not resolve executable path: %w", err)
	}

	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("could not resolve config path: %w", err)
	}

	data := unitData{
		Binary:     self,
		ConfigPath: absConfig,
		LogPath:    LogFilePath(absConfig),
		WorkDir:    resolveConfigDir(absConfig),
	}

	// Ensure systemd user directory exists
	unitDir := filepath.Dir(unitPath())
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		return fmt.Errorf("could not create systemd user directory: %w", err)
	}

	// Write unit file
	f, err := os.Create(unitPath())
	if err != nil {
		return fmt.Errorf("could not create unit file: %w", err)
	}
	defer f.Close()

	tmpl := template.Must(template.New("unit").Parse(unitTemplate))
	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("could not write unit file: %w", err)
	}

	// Enable and start
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "enable", unitName).Run(); err != nil {
		return fmt.Errorf("systemctl enable failed: %w", err)
	}

	return nil
}

// ServiceInstalled returns true if a systemd user unit exists.
func ServiceInstalled() bool {
	_, err := os.Stat(unitPath())
	return err == nil
}

// UninstallService disables and removes the systemd user unit.
func UninstallService() error {
	// Disable and stop (ignore errors if not active)
	exec.Command("systemctl", "--user", "disable", "--now", unitName).Run()

	// Remove unit file
	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not remove unit file: %w", err)
	}

	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}
