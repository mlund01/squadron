package cmd

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"squadron/internal/paths"
	squadronmcp "squadron/mcp"
	"squadron/plugin"
)

// squadronHomeOverride is the --squadron-home flag value. When set, it
// takes precedence over -c and SQUADRON_HOME for choosing the state
// directory. See paths.ResolveHome.
var squadronHomeOverride string

var rootCmd = &cobra.Command{
	Use:   "squadron",
	Short: "CLI for defining and running AI agents and multi-agent missions",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&squadronHomeOverride, "squadron-home", "",
		"Override the .squadron state directory (also via SQUADRON_HOME env var). "+
			"Default: <config>/.squadron when -c is given, else ./.squadron.")
}

// applyHome resolves the .squadron/ directory from the command's -c
// value, the --squadron-home flag, and the SQUADRON_HOME env var, then
// seeds paths.SquadronHome() so every state consumer (vault, DB,
// plugins, MCP cache) agrees on the location.
//
// Must be called before the first SquadronHome()-touching operation in
// a command. Calling it twice in one process is a no-op — paths caches
// the first resolution.
func applyHome(configPath string) error {
	home, err := paths.ResolveHome(configPath, squadronHomeOverride)
	if err != nil {
		return fmt.Errorf("resolve squadron home: %w", err)
	}
	return paths.SetHome(home)
}

func Execute() {
	// Ensure all plugins and MCP servers are cleaned up on exit
	defer plugin.CloseAll()
	defer squadronmcp.CloseAll()

	// Also clean up on signals (SIGKILL can't be caught, but SIGINT/SIGTERM can)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		squadronmcp.CloseAll()
		plugin.CloseAll()
		os.Exit(1)
	}()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
