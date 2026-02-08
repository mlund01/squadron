package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"squadron/config"
	"squadron/streamers/cli"
	"squadron/workflow"

	"github.com/spf13/cobra"
)

var inputFlags []string
var workflowDebugMode bool

var workflowCmd = &cobra.Command{
	Use:   "workflow [workflow_name]",
	Short: "Run a workflow",
	Long:  `Execute a workflow by name. The workflow will run all tasks respecting their dependencies, executing independent tasks in parallel. Provide inputs with --input key=value flags.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		workflowName := args[0]
		ctx := context.Background()

		// Load config
		cfg, err := config.LoadAndValidate(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}

		// Parse input flags into map
		inputs, err := parseInputFlags(inputFlags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing inputs: %v\n", err)
			os.Exit(1)
		}

		// Create debug logger if debug mode is enabled
		var debugDir string
		if workflowDebugMode {
			debugDir = filepath.Join("debug", fmt.Sprintf("%s_%s", workflowName, time.Now().Format("20060102_150405")))
		}
		debugLogger, err := workflow.NewDebugLogger(debugDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating debug logger: %v\n", err)
			os.Exit(1)
		}
		defer debugLogger.Close()

		if debugLogger.IsEnabled() {
			fmt.Printf("Debug mode enabled. Writing to: %s\n", debugLogger.GetDebugDir())
		}

		// Create workflow runner
		runner, err := workflow.NewRunner(cfg, configPath, workflowName, inputs, workflow.WithDebugLogger(debugLogger))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Create handler
		streamer := cli.NewWorkflowHandler()

		// Run the workflow
		if err := runner.Run(ctx, streamer); err != nil {
			fmt.Fprintf(os.Stderr, "\nWorkflow failed: %v\n", err)
			os.Exit(1)
		}
	},
}

// parseInputFlags parses --input key=value flags into a map
func parseInputFlags(flags []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, flag := range flags {
		parts := strings.SplitN(flag, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid input format '%s': expected key=value", flag)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			return nil, fmt.Errorf("invalid input format '%s': empty key", flag)
		}
		result[key] = value
	}
	return result, nil
}

func init() {
	rootCmd.AddCommand(workflowCmd)
	workflowCmd.Flags().StringVarP(&configPath, "config", "c", ".", "Path to config file or directory")
	workflowCmd.Flags().StringArrayVarP(&inputFlags, "input", "i", nil, "Workflow input in key=value format (can be repeated)")
	workflowCmd.Flags().BoolVarP(&workflowDebugMode, "debug", "d", false, "Enable debug mode to capture LLM messages and events")
}
