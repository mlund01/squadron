// Package gateway is squadron's host runtime for managed gateway
// subprocesses. Gateways are configured in HCL, downloaded from a
// GitHub release on first load (mirroring the native plugin install
// path), and run as subprocesses for the squadron process's lifetime.
//
// At the protocol level we use github.com/mlund01/squadron-gateway-sdk:
// gateways implement its Gateway interface and the SDK's Serve helper,
// squadron drives them through the SDK's HostPlugin / GRPCGatewayClient.
package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"squadron/internal/paths"
	"squadron/internal/release"
)

// dirRoot returns the platform-specific root for cached gateway
// binaries. Mirrors the layout of plugin/<platform>/<name>/<version>/.
func dirRoot() (string, error) {
	sqHome, err := paths.SquadronHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(sqHome, "gateways", paths.PlatformDir()), nil
}

// gatewayDir returns the cache directory for a specific gateway
// version. The binary lives at <dir>/gateway (or gateway.exe).
func gatewayDir(name, version string) (string, error) {
	root, err := dirRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name, version), nil
}

// gatewayBinary returns the absolute path to the gateway executable
// for a given name+version.
func gatewayBinary(name, version string) (string, error) {
	dir, err := gatewayDir(name, version)
	if err != nil {
		return "", err
	}
	bin := "gateway"
	if runtime.GOOS == "windows" {
		bin = "gateway.exe"
	}
	return filepath.Join(dir, bin), nil
}

// ensureInstalled returns the absolute path to a gateway binary,
// downloading and extracting from the configured GitHub source on
// first load. If the binary already exists at the cached path it is
// reused — same idempotency guarantee plugins offer.
func ensureInstalled(name, version, source string) (string, error) {
	bin, err := gatewayBinary(name, version)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	if source == "" {
		return "", fmt.Errorf("gateway %q v%s not installed at %s and no source configured", name, version, bin)
	}

	src, err := release.ParseGitHubSource(source)
	if err != nil {
		return "", fmt.Errorf("gateway %q: %w", name, err)
	}
	dir, err := gatewayDir(name, version)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("gateway %q: create cache dir: %w", name, err)
	}

	archiveName, archiveURL, checksumURL := release.ArchiveURLs(src, version)
	expected, err := release.FetchChecksum(checksumURL, archiveName)
	if err != nil {
		return "", fmt.Errorf("gateway %q: fetch checksum: %w", name, err)
	}
	tmp, err := release.DownloadToTemp(archiveURL)
	if err != nil {
		return "", fmt.Errorf("gateway %q: download: %w", name, err)
	}
	defer os.Remove(tmp)
	if err := release.VerifyChecksum(tmp, expected); err != nil {
		return "", fmt.Errorf("gateway %q: %w", name, err)
	}

	// Keep only the binary itself — gateways ship a single
	// `gateway` executable per release, no auxiliary files yet.
	binBase := filepath.Base(bin)
	filter := func(header string) string {
		if filepath.Base(header) == binBase {
			return binBase
		}
		return ""
	}
	var extractedCount int
	if runtime.GOOS == "windows" {
		extractedCount, err = release.ExtractZip(tmp, dir, filter)
	} else {
		extractedCount, err = release.ExtractTarGz(tmp, dir, filter)
	}
	if err != nil {
		return "", fmt.Errorf("gateway %q: extract: %w", name, err)
	}
	if extractedCount == 0 {
		return "", fmt.Errorf("gateway %q: archive contained no %q binary", name, binBase)
	}
	if err := os.Chmod(bin, 0755); err != nil {
		return "", fmt.Errorf("gateway %q: chmod: %w", name, err)
	}
	return bin, nil
}
