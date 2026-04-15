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

const banner = ` ███████  ██████  ██    ██  █████  ██████  ██████   ██████  ███    ██  ✈
 ██      ██    ██ ██    ██ ██   ██ ██   ██ ██   ██ ██    ██ ████   ██   ✈
 ███████ ██    ██ ██    ██ ███████ ██   ██ ██████  ██    ██ ██ ██  ██    ✈
      ██ ██ ██ ██ ██    ██ ██   ██ ██   ██ ██   ██ ██    ██ ██  ██ ██   ✈
 ███████  ██████   ██████  ██   ██ ██████  ██   ██  ██████  ██   ████  ✈`

func printBanner() {
	fmt.Println()
	fmt.Println(banner)
	fmt.Println()
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	rootCmd.Long = fmt.Sprintf(`
%s

 %s

HCL-based CLI for defining and running AI agents and multi-agent missions.

Define agents, models, tools, and missions in HCL configuration files,
then run them with simple commands.

Get started:
  squadron quickstart     Interactive setup wizard
  squadron engage         Start Squadron with command center UI
  squadron verify <path>  Validate your configuration
  squadron chat <agent>   Chat with an agent
  squadron mission <name> Run a mission`, banner, Version)
}
