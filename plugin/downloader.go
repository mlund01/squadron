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

	assets, _ := release.ListAssets(src, version)
	if wheel := findWheel(assets); wheel != nil {
		return downloadAndInstallWheel(*wheel, assets, destDir)
	}

	return downloadGoBinary(src, version, destDir)
}

func findWheel(assets []release.Asset) *release.Asset {
	for i, a := range assets {
		if strings.HasSuffix(a.Name, "-py3-none-any.whl") {
			return &assets[i]
		}
	}
	for i, a := range assets {
		if strings.HasSuffix(a.Name, ".whl") {
			return &assets[i]
		}
	}
	return nil
}

func findChecksums(assets []release.Asset) *release.Asset {
	for i, a := range assets {
		if a.Name == "checksums.txt" {
			return &assets[i]
		}
	}
	return nil
}

func downloadAndInstallWheel(wheel release.Asset, assets []release.Asset, destDir string) error {
	checksums := findChecksums(assets)
	if checksums == nil {
		return fmt.Errorf("wheel %s present but checksums.txt missing from release — refusing to install unverified wheel", wheel.Name)
	}

	expected, err := release.FetchChecksum(checksums.DownloadURL, wheel.Name)
	if err != nil {
		return fmt.Errorf("fetch checksum for %s: %w", wheel.Name, err)
	}

	fmt.Printf("Downloading wheel %s...\n", wheel.Name)
	wheelPath, err := downloadToFile(wheel.DownloadURL, filepath.Join(destDir, wheel.Name))
	if err != nil {
		return fmt.Errorf("download wheel: %w", err)
	}
	defer os.Remove(wheelPath)

	if err := release.VerifyChecksum(wheelPath, expected); err != nil {
		return fmt.Errorf("wheel %s checksum verification failed: %w", wheel.Name, err)
	}

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
