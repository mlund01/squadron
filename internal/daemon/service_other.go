//go:build !darwin && !linux

package daemon

import "fmt"

// InstallService is not supported on this platform.
func InstallService(configPath string) error {
	return fmt.Errorf("system service install is not supported on this platform")
}

// ServiceInstalled always returns false on unsupported platforms.
func ServiceInstalled() bool {
	return false
}

// UninstallService is not supported on this platform.
func UninstallService() error {
	return fmt.Errorf("system service uninstall is not supported on this platform")
}
