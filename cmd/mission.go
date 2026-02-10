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
	"squadron/mission"

	"github.com/spf13/cobra"
)

var inputFlags []string
var missionDebugMode bool

var missionCmd = &cobra.Command{
	Use:   "mission [mission_name]",
	Short: "Run a mission",
	Long:  `Execute a mission by name. The mission will run all tasks respecting their dependencies, executing independent tasks in parallel. Provide inputs with --input key=value flags.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		missionName := args[0]
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
		if missionDebugMode {
			debugDir = filepath.Join("debug", fmt.Sprintf("%s_%s", missionName, time.Now().Format("20060102_150405")))
		}
		debugLogger, err := mission.NewDebugLogger(debugDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating debug logger: %v\n", err)
			os.Exit(1)
		}
		defer debugLogger.Close()

		if debugLogger.IsEnabled() {
			fmt.Printf("Debug mode enabled. Writing to: %s\n", debugLogger.GetDebugDir())
		}

		// Create mission runner
		runner, err := mission.NewRunner(cfg, configPath, missionName, inputs, mission.WithDebugLogger(debugLogger))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Create handler
		streamer := cli.NewMissionHandler()

		// Run the mission
		if err := runner.Run(ctx, streamer); err != nil {
			fmt.Fprintf(os.Stderr, "\nMission failed: %v\n", err)
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
	rootCmd.AddCommand(missionCmd)
	missionCmd.Flags().StringVarP(&configPath, "config", "c", ".", "Path to config file or directory")
	missionCmd.Flags().StringArrayVarP(&inputFlags, "input", "i", nil, "Mission input in key=value format (can be repeated)")
	missionCmd.Flags().BoolVarP(&missionDebugMode, "debug", "d", false, "Enable debug mode to capture LLM messages and events")
}
