//go:build darwin

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const plistLabel = "com.squadron.engage"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>
        <string>engage</string>
        <string>--foreground</string>
        <string>-c</string>
        <string>{{.ConfigPath}}</string>
        <string>--headless</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
    <key>WorkingDirectory</key>
    <string>{{.WorkDir}}</string>
</dict>
</plist>
`

type plistData struct {
	Label      string
	Binary     string
	ConfigPath string
	LogPath    string
	WorkDir    string
}

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
}

// InstallService generates a launchd plist and loads it.
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

	data := plistData{
		Label:      plistLabel,
		Binary:     self,
		ConfigPath: absConfig,
		LogPath:    LogFilePath(absConfig),
		WorkDir:    resolveConfigDir(absConfig),
	}

	// Ensure LaunchAgents directory exists
	plistDir := filepath.Dir(plistPath())
	if err := os.MkdirAll(plistDir, 0755); err != nil {
		return fmt.Errorf("could not create LaunchAgents directory: %w", err)
	}

	// Write plist
	f, err := os.Create(plistPath())
	if err != nil {
		return fmt.Errorf("could not create plist: %w", err)
	}
	defer f.Close()

	tmpl := template.Must(template.New("plist").Parse(plistTemplate))
	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("could not write plist: %w", err)
	}

	// Load the service (suppress stderr — launchctl can emit warnings)
	cmd := exec.Command("launchctl", "load", plistPath())
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w", err)
	}

	return nil
}

// ServiceInstalled returns true if a launchd plist exists.
func ServiceInstalled() bool {
	_, err := os.Stat(plistPath())
	return err == nil
}

// UninstallService unloads and removes the launchd plist.
func UninstallService() error {
	path := plistPath()

	// Unload (ignore error if not loaded)
	exec.Command("launchctl", "unload", path).Run()

	// Remove plist file
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not remove plist: %w", err)
	}

	return nil
}
