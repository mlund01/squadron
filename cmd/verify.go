package cmd

import (
	"fmt"
	"os"

	"squadron/config"
	"squadron/gateway"

	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify [path]",
	Short: "Verify that the configuration is valid",
	Long:  `Verify parses and validates the HCL configuration files. Path can be a file or directory.`,
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		configPath := "."
		if len(args) > 0 {
			configPath = args[0]
		}
		if err := applyHome(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		cfg, err := config.LoadAndValidate(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Mirror the plugin install behavior: configured gateways must be
		// installable, otherwise verify fails. EnsureInstalled covers both
		// the github-source path (download + checksum + extract) and the
		// version="local" path (must already exist at the cached path).
		gatewayBinaryPath := ""
		if cfg.Gateway != nil {
			bin, err := gateway.EnsureInstalled(cfg.Gateway.Name, cfg.Gateway.Version, cfg.Gateway.Source)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: gateway %q failed to install: %v\n", cfg.Gateway.Name, err)
				os.Exit(1)
			}
			gatewayBinaryPath = bin
		}

		// Check for unset variables
		var warnings []string
		for _, v := range cfg.Variables {
			resolved, _ := config.ResolveVariableValue(&v)
			if resolved == "" && v.Default == "" {
				warnings = append(warnings, fmt.Sprintf("variable '%s' has no default and no value set", v.Name))
			}
		}

		fmt.Printf("Configuration is valid!\n")
		fmt.Printf("Storage: %s", cfg.Storage.Backend)
		if cfg.Storage.Backend == "sqlite" {
			fmt.Printf(" (%s)", cfg.Storage.Path)
		}
		fmt.Println()
		fmt.Printf("Found %d model(s)\n", len(cfg.Models))
		for _, m := range cfg.Models {
			available := m.AvailableModels()
			keys := make([]string, 0, len(available))
			for k := range available {
				keys = append(keys, k)
			}
			fmt.Printf("  - %s (provider: %s, models: %v)\n", m.Name, m.Provider, keys)
		}
		fmt.Printf("Found %d variable(s)\n", len(cfg.Variables))
		for _, v := range cfg.Variables {
			resolved, _ := config.ResolveVariableValue(&v)
			if v.Secret {
				if resolved != "" {
					fmt.Printf("  - %s (secret, set)\n", v.Name)
				} else {
					fmt.Printf("  - %s (secret, not set)\n", v.Name)
				}
			} else {
				fmt.Printf("  - %s = %q\n", v.Name, resolved)
			}
		}
		fmt.Printf("Found %d agent(s)\n", len(cfg.Agents))
		for _, a := range cfg.Agents {
			toolInfo := "no tools"
			if len(a.Tools) > 0 {
				toolInfo = fmt.Sprintf("tools: %v", a.Tools)
			}
			fmt.Printf("  - %s (%s)\n", a.Name, toolInfo)
		}
		fmt.Printf("Found %d custom tool(s)\n", len(cfg.CustomTools))
		for _, t := range cfg.CustomTools {
			var inputNames []string
			if t.Inputs != nil {
				for _, f := range t.Inputs.Fields {
					inputNames = append(inputNames, f.Name)
				}
			}
			fmt.Printf("  - %s (implements: %s, inputs: %v)\n", t.Name, t.Implements, inputNames)
		}
		fmt.Printf("Found %d plugin(s)\n", len(cfg.Plugins))
		for _, p := range cfg.Plugins {
			loaded := "loaded"
			if _, ok := cfg.LoadedPlugins[p.Name]; !ok {
				loaded = "NOT LOADED"
			}
			fmt.Printf("  - %s (source: %s, version: %s, %s)\n", p.Name, p.Source, p.Version, loaded)
		}
		fmt.Printf("Found %d mcp server(s)\n", len(cfg.MCPServers))
		for _, s := range cfg.MCPServers {
			// A nil entry in LoadedMCPClients means load tolerated an
			// AuthRequiredError — the block parsed fine but the server
			// isn't authorized yet.
			status := "NOT LOADED"
			if client, ok := cfg.LoadedMCPClients[s.Name]; ok {
				if client == nil {
					status = "needs login"
				} else {
					status = "loaded"
				}
			}
			fmt.Printf("  - %s (%s, %s)\n", s.Name, s.Location(), status)
		}
		gatewayCount := 0
		if cfg.Gateway != nil {
			gatewayCount = 1
		}
		fmt.Printf("Found %d gateway(s)\n", gatewayCount)
		if cfg.Gateway != nil {
			fmt.Printf("  - %s (source: %s, version: %s, installed at %s)\n", cfg.Gateway.Name, cfg.Gateway.Source, cfg.Gateway.Version, gatewayBinaryPath)
		}
		fmt.Printf("Found %d mission(s)\n", len(cfg.Missions))
		for _, w := range cfg.Missions {
			fmt.Printf("  - %s (commander: %s, agents: %v, tasks: %d)\n", w.Name, w.Commander.Model, w.Agents, len(w.Tasks))
			for _, t := range w.Tasks {
				deps := ""
				if len(t.DependsOn) > 0 {
					deps = fmt.Sprintf(" [depends_on: %v]", t.DependsOn)
				}
				taskAgents := "inherits mission agents"
				if len(t.Agents) > 0 {
					taskAgents = fmt.Sprintf("%v", t.Agents)
				}
				fmt.Printf("      • %s (agents: %s)%s\n", t.Name, taskAgents, deps)
			}
		}

		if len(warnings) > 0 {
			fmt.Printf("\nWarnings:\n")
			for _, w := range warnings {
				fmt.Printf("  - %s\n", w)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(verifyCmd)
}
