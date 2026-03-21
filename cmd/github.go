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
)

// githubRelease is the subset of GitHub API release response we need.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func releasesURL(owner, repo string) string {
	return "https://api.github.com/repos/" + owner + "/" + repo + "/releases"
}

func fetchLatestRelease(owner, repo string) (githubRelease, error) {
	return fetchReleaseFromURL(releasesURL(owner, repo) + "/latest")
}

func fetchRelease(owner, repo, tag string) (githubRelease, error) {
	return fetchReleaseFromURL(releasesURL(owner, repo) + "/tags/" + tag)
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

// findAssetURL finds the download URL for a platform-specific asset in a release.
func findAssetURL(release githubRelease, projectName string) (string, error) {
	assetName := fmt.Sprintf("%s_%s_%s.tar.gz", projectName, runtime.GOOS, runtime.GOARCH)
	for _, a := range release.Assets {
		if a.Name == assetName {
			return a.BrowserDownloadURL, nil
		}
	}
	var available []string
	for _, a := range release.Assets {
		available = append(available, a.Name)
	}
	return "", fmt.Errorf("no release asset found for %s/%s (looking for %s)\nAvailable: %s",
		runtime.GOOS, runtime.GOARCH, assetName, strings.Join(available, ", "))
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

	tmp, err := os.CreateTemp("", "squadron-download-*.tar.gz")
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

// extractBinaryFromArchive extracts a named binary from a tar.gz archive.
func extractBinaryFromArchive(archivePath, binaryName string) (string, error) {
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

		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == binaryName {
			tmp, err := os.CreateTemp("", binaryName+"-*")
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

	return "", fmt.Errorf("binary '%s' not found in archive", binaryName)
}
