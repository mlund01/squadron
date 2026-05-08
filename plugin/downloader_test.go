package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"squadron/internal/release"
)

func TestDownloadAndInstallWheel_RejectsWhenChecksumsMissing(t *testing.T) {
	wheel := release.Asset{Name: "myplug-0.1.0-py3-none-any.whl", DownloadURL: "http://example/whl"}
	assets := []release.Asset{wheel}

	err := downloadAndInstallWheel(wheel, assets, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "checksums.txt missing") {
		t.Fatalf("expected checksums-missing error, got %v", err)
	}
}

func TestDownloadAndInstallWheel_RejectsOnChecksumMismatch(t *testing.T) {
	wheelPath := "/tmp/wheel_out/myplug-0.1.0-py3-none-any.whl"
	if _, err := os.Stat(wheelPath); err != nil {
		t.Skipf("wheel not present at %s — skipping", wheelPath)
	}
	wheelBytes, _ := os.ReadFile(wheelPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/whl", func(w http.ResponseWriter, r *http.Request) {
		w.Write(wheelBytes)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "deadbeef0000000000000000000000000000000000000000000000000000beef  myplug-0.1.0-py3-none-any.whl\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wheel := release.Asset{Name: "myplug-0.1.0-py3-none-any.whl", DownloadURL: srv.URL + "/whl"}
	checksums := release.Asset{Name: "checksums.txt", DownloadURL: srv.URL + "/checksums.txt"}

	err := downloadAndInstallWheel(wheel, []release.Asset{wheel, checksums}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("expected checksum verification failure, got %v", err)
	}
}

func TestDownloadAndInstallWheel_HappyPath(t *testing.T) {
	wheelPath := "/tmp/wheel_out/myplug-0.1.0-py3-none-any.whl"
	if _, err := os.Stat(wheelPath); err != nil {
		t.Skipf("wheel not present at %s — skipping", wheelPath)
	}
	wheelBytes, _ := os.ReadFile(wheelPath)
	sum := sha256.Sum256(wheelBytes)
	expectedHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/whl", func(w http.ResponseWriter, r *http.Request) {
		w.Write(wheelBytes)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  myplug-0.1.0-py3-none-any.whl\n", expectedHex)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wheel := release.Asset{Name: "myplug-0.1.0-py3-none-any.whl", DownloadURL: srv.URL + "/whl"}
	checksums := release.Asset{Name: "checksums.txt", DownloadURL: srv.URL + "/checksums.txt"}

	dest := t.TempDir()
	if err := downloadAndInstallWheel(wheel, []release.Asset{wheel, checksums}, dest); err != nil {
		t.Fatalf("install: %v", err)
	}

	runner, ok := readRunner(dest)
	if !ok {
		t.Fatal("runner.json not written after successful wheel install")
	}
	if runner.Kind != "python" {
		t.Errorf("kind = %q, want python", runner.Kind)
	}

	scriptPath := filepath.Join(dest, runner.Entry)
	if _, err := os.Stat(scriptPath); err != nil {
		t.Errorf("script %s not present after install: %v", scriptPath, err)
	}
}
