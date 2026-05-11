package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// hashSource returns a sha256 over the contents (and relative paths) of
// every file under sourcePath, skipping directories and files that
// don't influence the build output: VCS data, virtualenvs, caches, and
// already-compiled artifacts.
//
// The hash is stable across runs as long as the inputs to the build
// haven't changed. It does not follow symlinks — filepath.WalkDir
// reports them as entries but doesn't descend into them, which is the
// behavior we want (treating symlink contents as opaque avoids loops
// and keeps the hash deterministic).
func hashSource(sourcePath string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(sourcePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(sourcePath, path)
		if relErr != nil {
			return relErr
		}
		if shouldIgnoreForHash(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}

		// Mix the relative path in so file moves change the hash.
		fmt.Fprintf(h, "%s\x00", filepath.ToSlash(rel))

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// shouldIgnoreForHash filters out directories and files that don't
// affect build output, so users can have an editor open, a venv on
// disk, or a stale .pyc lying around without forcing a rebuild.
func shouldIgnoreForHash(rel string, d fs.DirEntry) bool {
	if rel == "." {
		return false
	}
	base := filepath.Base(rel)
	if d.IsDir() {
		switch base {
		case ".git", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache",
			"node_modules", "venv", ".venv", "dist", "build", ".tox", ".idea", ".vscode":
			return true
		}
		// Python's editable-install marker dirs.
		if strings.HasSuffix(base, ".egg-info") {
			return true
		}
		return false
	}
	// Files.
	if strings.HasSuffix(base, ".pyc") || strings.HasSuffix(base, ".pyo") {
		return true
	}
	if base == ".DS_Store" {
		return true
	}
	return false
}
