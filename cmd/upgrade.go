package cmd

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const (
	githubOwner = "mlund01"
	githubRepo  = "squadron"
	releasesAPI = "https://api.github.com/repos/" + githubOwner + "/" + githubRepo + "/releases"
)

var upgradeVersion string

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade squadron to the latest version",
	Long:  `Download and install the latest squadron binary from GitHub releases. Use --version to install a specific version.`,
	RunE:  runUpgrade,
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
	upgradeCmd.Flags().StringVar(&upgradeVersion, "version", "", "Specific version to install (e.g., v0.0.12)")
}

// githubRelease is the subset of GitHub API release response we need
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	// 1. Determine target version
	var release githubRelease
	var err error

	if upgradeVersion != "" {
		// Normalize: ensure "v" prefix
		tag := upgradeVersion
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		release, err = fetchRelease(tag)
	} else {
		release, err = fetchLatestRelease()
	}
	if err != nil {
		return err
	}

	targetVersion := strings.TrimPrefix(release.TagName, "v")

	// 2. Check if already up to date
	currentVersion := strings.TrimPrefix(Version, "v")
	if currentVersion == targetVersion {
		fmt.Printf("Already up to date (v%s)\n", targetVersion)
		return nil
	}

	if Version == "dev" {
		fmt.Println("Warning: current version is a dev build. Upgrading to release version.")
	}

	fmt.Printf("Upgrading: %s → v%s\n", Version, targetVersion)

	// 3. Find the right asset
	assetName := fmt.Sprintf("squadron_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	var downloadURL string
	for _, a := range release.Assets {
		if a.Name == assetName {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		var available []string
		for _, a := range release.Assets {
			available = append(available, a.Name)
		}
		return fmt.Errorf("no release asset found for %s/%s (looking for %s)\nAvailable: %s",
			runtime.GOOS, runtime.GOARCH, assetName, strings.Join(available, ", "))
	}

	// 4. Download archive to temp file
	fmt.Printf("Downloading %s...\n", assetName)
	archivePath, err := downloadToTemp(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(archivePath)

	// 5. Extract binary from tar.gz
	binaryPath, err := extractBinary(archivePath)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	defer os.Remove(binaryPath)

	// 6. Replace current binary
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine current binary path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("could not resolve binary path: %w", err)
	}

	if err := replaceBinary(execPath, binaryPath); err != nil {
		return err
	}

	fmt.Printf("Successfully upgraded to v%s\n", targetVersion)
	return nil
}

func fetchLatestRelease() (githubRelease, error) {
	return fetchReleaseFromURL(releasesAPI + "/latest")
}

func fetchRelease(tag string) (githubRelease, error) {
	return fetchReleaseFromURL(releasesAPI + "/tags/" + tag)
}

func fetchReleaseFromURL(url string) (githubRelease, error) {
	var release githubRelease

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return release, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return release, fmt.Errorf("failed to reach GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return release, fmt.Errorf("release not found: %s", url)
	}
	if resp.StatusCode != 200 {
		return release, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return release, fmt.Errorf("failed to parse release info: %w", err)
	}
	return release, nil
}

func downloadToTemp(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "squadron-upgrade-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	return tmp.Name(), nil
}

func extractBinary(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("not a valid gzip archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		// Look for the squadron binary (may be at root or in a subdirectory)
		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == "squadron" {
			tmp, err := os.CreateTemp("", "squadron-bin-*")
			if err != nil {
				return "", err
			}

			if _, err := io.Copy(tmp, tr); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return "", err
			}
			tmp.Close()

			if err := os.Chmod(tmp.Name(), 0755); err != nil {
				os.Remove(tmp.Name())
				return "", err
			}

			return tmp.Name(), nil
		}
	}

	return "", fmt.Errorf("binary 'squadron' not found in archive")
}

func replaceBinary(target, newBinary string) error {
	oldPath := target + ".old"

	// Remove any leftover .old file from a previous upgrade
	os.Remove(oldPath)

	// Atomic swap: rename current → .old, rename new → target
	if err := os.Rename(target, oldPath); err != nil {
		return fmt.Errorf("could not replace binary at %s: %w\nTry: sudo squadron upgrade", target, err)
	}

	if err := os.Rename(newBinary, target); err != nil {
		// Rollback: restore the old binary
		os.Rename(oldPath, target)
		return fmt.Errorf("could not install new binary: %w", err)
	}

	// Clean up the old binary
	os.Remove(oldPath)
	return nil
}
