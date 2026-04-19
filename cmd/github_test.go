package cmd

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFindChecksumsURL(t *testing.T) {
	rel := githubRelease{
		TagName: "v1.2.3",
		Assets: []githubAsset{
			{Name: "squadron_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/archive"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
		},
	}
	got, err := findChecksumsURL(rel)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/checksums.txt" {
		t.Fatalf("unexpected URL: %s", got)
	}

	_, err = findChecksumsURL(githubRelease{TagName: "v1.2.3"})
	if err == nil {
		t.Fatal("expected error when checksums.txt missing")
	}
}

func TestDownloadAndVerify(t *testing.T) {
	archive := []byte("pretend archive bytes")
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))

	mux := http.NewServeMux()
	mux.HandleFunc("/squadron_linux_amd64.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	var checksumBody string
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumBody))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := githubRelease{
		TagName: "v1.0.0",
		Assets: []githubAsset{
			{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums.txt"},
		},
	}
	archiveURL := srv.URL + "/squadron_linux_amd64.tar.gz"

	checksumBody = sum + "  squadron_linux_amd64.tar.gz\n"
	path, err := downloadAndVerify(rel, archiveURL)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	defer os.Remove(path)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("verified archive missing: %v", err)
	}

	checksumBody = strings.Repeat("f", 64) + "  squadron_linux_amd64.tar.gz\n"
	badPath, err := downloadAndVerify(rel, archiveURL)
	if err == nil {
		os.Remove(badPath)
		t.Fatal("expected checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
	if badPath != "" {
		if _, statErr := os.Stat(badPath); statErr == nil {
			t.Fatal("unverified archive was left on disk")
		}
	}
}
