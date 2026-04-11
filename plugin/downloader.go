package plugin

import (
	"fmt"
	"path/filepath"
	"runtime"

	"squadron/internal/release"
)

// DownloadPlugin downloads a plugin archive from GitHub releases and extracts
// the "plugin" (or "plugin.exe") binary into destDir.
func DownloadPlugin(source, version, destDir string) error {
	src, err := release.ParseGitHubSource(source)
	if err != nil {
		return err
	}

	want := "plugin"
	if runtime.GOOS == "windows" {
		want = "plugin.exe"
	}

	// Filter picks only the file named "plugin"/"plugin.exe" and places it at
	// the top of destDir (flattening any nested directory structure).
	filter := func(header string) string {
		if filepath.Base(header) == want {
			return want
		}
		return ""
	}

	count, err := release.DownloadAndExtract(src, version, destDir, filter)
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("plugin binary %q not found in archive", want)
	}
	return nil
}
