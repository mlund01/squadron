package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "squadron",
	Short: "Squad is a CLI tool",
	Long:  `Squad is a command-line interface tool built with Cobra.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Welcome to Squad! Use --help to see available commands.")
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
