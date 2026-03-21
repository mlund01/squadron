package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"squadron/plugin"
)

var rootCmd = &cobra.Command{
	Use:   "squadron",
	Short: "CLI for defining and running AI agents and multi-agent missions",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func Execute() {
	// Ensure all plugins are cleaned up on exit
	defer plugin.CloseAll()

	// Also clean up plugins on signals (SIGKILL can't be caught, but SIGINT/SIGTERM can)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		plugin.CloseAll()
		os.Exit(1)
	}()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
