package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	squadronGitHubOwner = "mlund01"
	squadronGitHubRepo  = "squadron"
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

func runUpgrade(cmd *cobra.Command, args []string) error {
	// 1. Determine target version
	var release githubRelease
	var err error

	if upgradeVersion != "" {
		tag := upgradeVersion
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		release, err = fetchRelease(squadronGitHubOwner, squadronGitHubRepo, tag)
	} else {
		release, err = fetchLatestRelease(squadronGitHubOwner, squadronGitHubRepo)
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
	downloadURL, err := findAssetURL(release, "squadron")
	if err != nil {
		return err
	}

	// 4. Download archive to temp file
	fmt.Println("Downloading...")
	archivePath, err := downloadToTemp(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(archivePath)

	// 5. Extract binary from tar.gz
	binaryPath, err := extractBinaryFromArchive(archivePath, "squadron")
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
