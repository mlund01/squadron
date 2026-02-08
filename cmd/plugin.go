package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"squad/plugin"
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Plugin management commands",
	Long:  `Commands for managing and testing plugins.`,
}

var pluginCallCmd = &cobra.Command{
	Use:   "call <plugin-name> <tool-name> [payload]",
	Short: "Call a tool on a plugin",
	Long:  `Call a tool on a plugin with an optional JSON payload.`,
	Args:  cobra.RangeArgs(2, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		pluginName := args[0]
		toolName := args[1]
		payload := ""
		if len(args) > 2 {
			payload = args[2]
		}

		version, _ := cmd.Flags().GetString("version")

		// Load the plugin (no source for CLI commands - must be installed locally)
		p, err := plugin.LoadPlugin(pluginName, version, "")
		if err != nil {
			return fmt.Errorf("failed to load plugin: %w", err)
		}
		defer p.Close()

		// Call the tool
		result, err := p.Call(toolName, payload)
		if err != nil {
			return fmt.Errorf("plugin call failed: %w", err)
		}

		fmt.Println(result)
		return nil
	},
}

var pluginToolsCmd = &cobra.Command{
	Use:   "tools <plugin-name>",
	Short: "List available tools on a plugin",
	Long:  `List all available tools that a plugin provides with their descriptions.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pluginName := args[0]
		version, _ := cmd.Flags().GetString("version")

		// Load the plugin (no source for CLI commands - must be installed locally)
		p, err := plugin.LoadPlugin(pluginName, version, "")
		if err != nil {
			return fmt.Errorf("failed to load plugin: %w", err)
		}
		defer p.Close()

		// Get tools
		tools, err := p.ListTools()
		if err != nil {
			return fmt.Errorf("failed to list tools: %w", err)
		}

		fmt.Printf("Available tools for plugin '%s':\n", pluginName)
		for _, t := range tools {
			fmt.Printf("  - %s: %s\n", t.Name, t.Description)
		}
		return nil
	},
}

var pluginInfoCmd = &cobra.Command{
	Use:   "info <plugin-name> <tool-name>",
	Short: "Get detailed info about a tool",
	Long:  `Get detailed information about a specific tool including its schema.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		pluginName := args[0]
		toolName := args[1]
		version, _ := cmd.Flags().GetString("version")

		// Load the plugin (no source for CLI commands - must be installed locally)
		p, err := plugin.LoadPlugin(pluginName, version, "")
		if err != nil {
			return fmt.Errorf("failed to load plugin: %w", err)
		}
		defer p.Close()

		// Get tool info
		info, err := p.GetToolInfo(toolName)
		if err != nil {
			return fmt.Errorf("failed to get tool info: %w", err)
		}

		fmt.Printf("Tool: %s\n", info.Name)
		fmt.Printf("Description: %s\n", info.Description)
		fmt.Printf("Schema: %s\n", info.Schema.String())
		return nil
	},
}

var pluginBuildCmd = &cobra.Command{
	Use:   "build <plugin-name> <source-path>",
	Short: "Build a plugin from source",
	Long:  `Build a plugin from a Go source directory and install it to ~/.squad/plugins/<name>/<version>/plugin`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		pluginName := args[0]
		sourcePath := args[1]

		version, _ := cmd.Flags().GetString("version")

		// Resolve source path to absolute
		absSourcePath, err := filepath.Abs(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to resolve source path: %w", err)
		}

		// Check source exists
		if _, err := os.Stat(absSourcePath); os.IsNotExist(err) {
			return fmt.Errorf("source path does not exist: %s", absSourcePath)
		}

		// Get the plugin directory
		pluginDir, err := plugin.GetPluginDir(pluginName, version)
		if err != nil {
			return fmt.Errorf("failed to get plugin directory: %w", err)
		}

		// Create the directory
		if err := os.MkdirAll(pluginDir, 0755); err != nil {
			return fmt.Errorf("failed to create plugin directory: %w", err)
		}

		// Get the output path
		outputPath := filepath.Join(pluginDir, "plugin")

		// Run go build
		fmt.Printf("Building plugin '%s' (version: %s)...\n", pluginName, version)
		fmt.Printf("  Source: %s\n", absSourcePath)
		fmt.Printf("  Output: %s\n", outputPath)

		buildCmd := exec.Command("go", "build", "-o", outputPath, absSourcePath)
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr

		if err := buildCmd.Run(); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}

		fmt.Printf("Plugin '%s' built successfully!\n", pluginName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pluginCmd)
	pluginCmd.AddCommand(pluginCallCmd)
	pluginCmd.AddCommand(pluginToolsCmd)
	pluginCmd.AddCommand(pluginInfoCmd)
	pluginCmd.AddCommand(pluginBuildCmd)

	// Add version flag to subcommands
	pluginCallCmd.Flags().StringP("version", "v", "local", "Plugin version to use")
	pluginToolsCmd.Flags().StringP("version", "v", "local", "Plugin version to use")
	pluginInfoCmd.Flags().StringP("version", "v", "local", "Plugin version to use")
	pluginBuildCmd.Flags().StringP("version", "v", "local", "Plugin version to install as")
}
