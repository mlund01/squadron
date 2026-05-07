package plugin

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"squadron/internal/release"
)

func DownloadPlugin(source, version, destDir string) error {
	src, err := release.ParseGitHubSource(source)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create plugin dir: %w", err)
	}

	wheel, err := findWheelAsset(src, version)
	if err != nil {
		return err
	}
	if wheel != nil {
		return downloadAndInstallWheel(*wheel, destDir)
	}

	return downloadGoBinary(src, version, destDir)
}

func findWheelAsset(src release.GitHubSource, version string) (*release.Asset, error) {
	assets, err := release.ListAssets(src, version)
	if err != nil {
		return nil, nil
	}
	for _, a := range assets {
		if strings.HasSuffix(a.Name, "-py3-none-any.whl") {
			return &a, nil
		}
	}
	for _, a := range assets {
		if strings.HasSuffix(a.Name, ".whl") {
			return &a, nil
		}
	}
	return nil, nil
}

func downloadAndInstallWheel(asset release.Asset, destDir string) error {
	fmt.Printf("Downloading wheel %s...\n", asset.Name)
	wheelPath, err := downloadToFile(asset.DownloadURL, filepath.Join(destDir, asset.Name))
	if err != nil {
		return fmt.Errorf("download wheel: %w", err)
	}
	defer os.Remove(wheelPath)

	scriptName, err := wheelScriptName(wheelPath)
	if err != nil {
		return err
	}

	return installPython(destDir, wheelPath, scriptName)
}

func downloadGoBinary(src release.GitHubSource, version, destDir string) error {
	want := "plugin"
	if runtime.GOOS == "windows" {
		want = "plugin.exe"
	}
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

	return writeRunner(destDir, &Runner{Kind: "go", Entry: want})
}

func downloadToFile(url, dest string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", err
	}
	return dest, nil
}
