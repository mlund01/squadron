package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"squadron/internal/daemon"
)

var disengageConfigPath string

var disengageCmd = &cobra.Command{
	Use:   "disengage",
	Short: "Stop Squadron and remove the system service",
	Long: `Stop a running Squadron background process and remove the system service
(launchd on macOS, systemd on Linux) so it no longer starts on boot.

This is the counterpart to 'squadron engage'.`,
	Run: func(cmd *cobra.Command, args []string) {
		didSomething := false

		running, pid := daemon.IsRunning(disengageConfigPath)
		if running {
			fmt.Printf("Stopping Squadron (PID %d)...\n", pid)
			if err := daemon.Stop(disengageConfigPath); err != nil {
				fmt.Fprintf(os.Stderr, "Error stopping process: %v\n", err)
			}
			didSomething = true
		}

		// Remove system service if one exists
		if daemon.ServiceInstalled() {
			if err := daemon.UninstallService(); err != nil {
				fmt.Fprintf(os.Stderr, "Error removing system service: %v\n", err)
			} else {
				fmt.Println("System service removed.")
			}
			didSomething = true
		}

		if didSomething {
			fmt.Println("Squadron disengaged.")
		} else {
			fmt.Println("Squadron is not running.")
		}
	},
}

func init() {
	rootCmd.AddCommand(disengageCmd)
	disengageCmd.Flags().StringVarP(&disengageConfigPath, "config", "c", ".", "Path to config file or directory")
}
