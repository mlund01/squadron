package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "squadron",
	Short: "CLI for defining and running AI agents and multi-agent workflows",
	Long: `Squadron is an HCL-based CLI for defining and running AI agents and multi-agent workflows.

Define agents, models, tools, and workflows in HCL configuration files,
then run them with simple commands.

Get started:
  squadron docs           Extract documentation to a local folder
  squadron verify <path>  Validate your configuration
  squadron chat <agent>   Chat with an agent
  squadron workflow <name> Run a workflow`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
