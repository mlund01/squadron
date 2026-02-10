package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	rootCmd.Long = fmt.Sprintf(`Squadron %s

HCL-based CLI for defining and running AI agents and multi-agent workflows.

Define agents, models, tools, and workflows in HCL configuration files,
then run them with simple commands.

Get started:
  squadron docs           Extract documentation to a local folder
  squadron verify <path>  Validate your configuration
  squadron chat <agent>   Chat with an agent
  squadron workflow <name> Run a workflow`, Version)
}
