package plugin

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DownloadPlugin downloads a plugin from GitHub releases
func DownloadPlugin(source, version, destDir string) error {
	owner, repo, err := parseGitHubSource(source)
	if err != nil {
		return err
	}

	// Build download URLs
	archiveName := fmt.Sprintf("%s_%s_%s.tar.gz", repo, runtime.GOOS, runtime.GOARCH)
	archiveURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		owner, repo, version, archiveName)
	checksumURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/checksums.txt",
		owner, repo, version)

	// Download and parse checksums
	expectedHash, err := fetchChecksum(checksumURL, archiveName)
	if err != nil {
		return fmt.Errorf("failed to fetch checksum: %w", err)
	}

	// Download archive to temp file
	tmpFile, err := downloadToTemp(archiveURL)
	if err != nil {
		return fmt.Errorf("failed to download archive: %w", err)
	}
	defer os.Remove(tmpFile)

	// Verify checksum
	if err := verifyChecksum(tmpFile, expectedHash); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Extract to destination
	if err := extractTarGz(tmpFile, destDir); err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}

	return nil
}

// parseGitHubSource extracts owner/repo from "github.com/owner/repo"
func parseGitHubSource(source string) (owner, repo string, err error) {
	source = strings.TrimPrefix(source, "https://")
	source = strings.TrimPrefix(source, "github.com/")
	parts := strings.Split(source, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid GitHub source: %s", source)
	}
	return parts[0], parts[1], nil
}

// fetchChecksum downloads checksums.txt and returns hash for the given file
func fetchChecksum(url, filename string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("checksum download failed: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Parse checksums.txt format: "hash  filename"
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == filename {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("checksum not found for %s", filename)
}

// downloadToTemp downloads URL to a temp file, returns path
func downloadToTemp(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", "plugin-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, resp.Body)
	return tmpFile.Name(), err
}

// verifyChecksum checks file against expected SHA256 hash
func verifyChecksum(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	actual := fmt.Sprintf("%x", h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("hash mismatch: got %s, want %s", actual, expected)
	}
	return nil
}

// extractTarGz extracts .tar.gz to destDir
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Only extract the binary named "plugin"
		if header.Name == "plugin" || strings.HasSuffix(header.Name, "/plugin") {
			if err := os.MkdirAll(destDir, 0755); err != nil {
				return err
			}
			outPath := filepath.Join(destDir, "plugin")
			outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY, 0755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
			return nil
		}
	}
	return fmt.Errorf("plugin binary not found in archive")
}
