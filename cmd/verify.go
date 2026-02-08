package cmd

import (
	"fmt"
	"os"

	"squadron/config"

	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify [path]",
	Short: "Verify that the configuration is valid",
	Long:  `Verify parses and validates the HCL configuration files. Path can be a file or directory.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		configPath := args[0]
		cfg, err := config.LoadAndValidate(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
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
		fmt.Printf("Found %d model(s)\n", len(cfg.Models))
		for _, m := range cfg.Models {
			fmt.Printf("  - %s (provider: %s, models: %v)\n", m.Name, m.Provider, m.AllowedModels)
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
		fmt.Printf("Found %d workflow(s)\n", len(cfg.Workflows))
		for _, w := range cfg.Workflows {
			fmt.Printf("  - %s (supervisor: %s, agents: %v, tasks: %d)\n", w.Name, w.SupervisorModel, w.Agents, len(w.Tasks))
			for _, t := range w.Tasks {
				deps := ""
				if len(t.DependsOn) > 0 {
					deps = fmt.Sprintf(" [depends_on: %v]", t.DependsOn)
				}
				taskAgents := "inherits workflow agents"
				if len(t.Agents) > 0 {
					taskAgents = fmt.Sprintf("%v", t.Agents)
				}
				fmt.Printf("      • %s (agents: %s)%s\n", t.Name, taskAgents, deps)
			}
		}

		// Add plugin warnings to the warnings list
		warnings = append(warnings, cfg.PluginWarnings...)

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
