// Package release provides shared helpers for downloading and extracting
// GitHub release archives. Both the native plugin system and the MCP consumer
// use these helpers; they differ only in how they pick which extracted files
// to keep.
package release

import (
	"archive/tar"
	"archive/zip"
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

// GitHubSource is a parsed "github.com/owner/repo" reference.
type GitHubSource struct {
	Owner string
	Repo  string
}

// ParseGitHubSource extracts owner/repo from "github.com/owner/repo".
func ParseGitHubSource(source string) (GitHubSource, error) {
	s := strings.TrimPrefix(source, "https://")
	s = strings.TrimPrefix(s, "github.com/")
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return GitHubSource{}, fmt.Errorf("invalid GitHub source: %s", source)
	}
	return GitHubSource{Owner: parts[0], Repo: parts[1]}, nil
}

// ArchiveURLs returns the archive and checksum URLs for a release asset named
// "<repo>_<GOOS>_<GOARCH>.tar.gz" (or .zip on Windows).
func ArchiveURLs(src GitHubSource, version string) (archiveName, archiveURL, checksumURL string) {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	archiveName = fmt.Sprintf("%s_%s_%s.%s", src.Repo, runtime.GOOS, runtime.GOARCH, ext)
	archiveURL = fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		src.Owner, src.Repo, version, archiveName)
	checksumURL = fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/checksums.txt",
		src.Owner, src.Repo, version)
	return
}

// FetchChecksum downloads checksums.txt and returns the SHA256 hash for the
// given filename.
func FetchChecksum(url, filename string) (string, error) {
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

	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == filename {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("checksum not found for %s", filename)
}

// DownloadToTemp downloads url to a temp file and returns its path. The caller
// is responsible for removing the file.
func DownloadToTemp(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", "release-*.bin")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, resp.Body)
	return tmpFile.Name(), err
}

// VerifyChecksum compares a file's SHA256 against the expected hash.
func VerifyChecksum(path, expected string) error {
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

// ExtractFilter controls which files in an archive get extracted.
//   - If the filter returns an empty string, the entry is skipped.
//   - Otherwise the entry is written to <destDir>/<returned path>. The returned
//     path should be relative and is passed through filepath.Clean.
type ExtractFilter func(header string) string

// ExtractTarGz extracts regular files from a .tar.gz archive into destDir,
// consulting filter for each entry. Returns the count of extracted files.
func ExtractTarGz(archivePath, destDir string, filter ExtractFilter) (int, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	count := 0

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}

		rel := filter(header.Name)
		if rel == "" {
			continue
		}
		if err := writeFileFromReader(destDir, rel, tr, fileModeFromHeader(header.Mode)); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// ExtractZip extracts regular files from a .zip archive into destDir,
// consulting filter for each entry. Returns the count of extracted files.
func ExtractZip(archivePath, destDir string, filter ExtractFilter) (int, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return 0, err
	}
	defer r.Close()

	count := 0
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel := filter(f.Name)
		if rel == "" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return count, err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0755
		}
		err = writeFileFromReader(destDir, rel, rc, mode)
		rc.Close()
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// writeFileFromReader writes r's contents to <destDir>/<rel> with the given
// mode, creating parent directories as needed. rel is cleaned and must not
// escape destDir.
func writeFileFromReader(destDir, rel string, r io.Reader, mode os.FileMode) error {
	cleaned := filepath.Clean(rel)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return fmt.Errorf("unsafe archive path: %s", rel)
	}
	out := filepath.Join(destDir, cleaned)
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func fileModeFromHeader(mode int64) os.FileMode {
	m := os.FileMode(mode) & os.ModePerm
	if m == 0 {
		m = 0755
	}
	return m
}

// DownloadAndExtract orchestrates the full flow: fetch checksum, download
// archive, verify, and extract into destDir using the supplied filter.
// It leaves the temp archive in place only on checksum-verification failure
// (so callers can inspect it); otherwise the temp file is removed.
func DownloadAndExtract(src GitHubSource, version, destDir string, filter ExtractFilter) (int, error) {
	archiveName, archiveURL, checksumURL := ArchiveURLs(src, version)

	expected, err := FetchChecksum(checksumURL, archiveName)
	if err != nil {
		return 0, fmt.Errorf("fetch checksum: %w", err)
	}

	tmp, err := DownloadToTemp(archiveURL)
	if err != nil {
		return 0, fmt.Errorf("download archive: %w", err)
	}
	defer os.Remove(tmp)

	if err := VerifyChecksum(tmp, expected); err != nil {
		return 0, fmt.Errorf("checksum verification failed: %w", err)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return 0, err
	}

	if strings.HasSuffix(archiveName, ".zip") {
		return ExtractZip(tmp, destDir, filter)
	}
	return ExtractTarGz(tmp, destDir, filter)
}
