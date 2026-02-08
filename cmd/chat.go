package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"squadron/agent"
	"squadron/config"
	"squadron/streamers/cli"
	"squadron/workflow"

	"github.com/spf13/cobra"
)

var configPath string
var debugMode bool
var workflowMode bool
var workflowTask string

var chatCmd = &cobra.Command{
	Use:   "chat [agent_name]",
	Short: "Chat with a given agent",
	Long:  `Start an interactive chat session with the specified agent.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		agentName := args[0]
		ctx := context.Background()

		// Build agent options
		opts := agent.Options{
			ConfigPath: configPath,
			AgentName:  agentName,
		}

		if workflowMode {
			mode := config.ModeWorkflow
			opts.Mode = &mode
		}

		// Create debug logger if debug mode is enabled
		var debugDir string
		if debugMode {
			debugDir = filepath.Join("debug", fmt.Sprintf("chat_%s_%s", agentName, time.Now().Format("20060102_150405")))
		}
		debugLogger, err := workflow.NewDebugLogger(debugDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating debug logger: %v\n", err)
			os.Exit(1)
		}
		defer debugLogger.Close()

		if debugLogger.IsEnabled() {
			fmt.Printf("Debug mode enabled. Writing to: %s\n", debugLogger.GetDebugDir())
			opts.DebugFile = debugLogger.GetMessageFile("agent", agentName)
			opts.TurnLogFile = debugLogger.GetTurnLogFile("agent", agentName)
			opts.EventLogger = debugLogger
		}

		// Create the agent
		a, err := agent.New(ctx, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer a.Close()

		// Create CLI handler
		streamer := cli.NewChatHandler()
		streamer.Welcome(a.Name, a.ModelName)

		// Workflow mode: non-interactive, run task to completion
		if workflowMode && workflowTask != "" {
			fmt.Printf("\n📋 Running workflow task: %s\n\n", workflowTask)
			_, _ = a.Chat(ctx, workflowTask, streamer)
			fmt.Println("\n✅ Workflow complete")
			return
		}

		// Chat mode: interactive REPL
		for {
			input, err := streamer.AwaitClientAnswer()
			if err != nil {
				if err == io.EOF {
					streamer.Goodbye()
					break
				}
				streamer.Error(err)
				break
			}

			if input == "" {
				continue
			}

			if input == "exit" || input == "quit" {
				streamer.Goodbye()
				break
			}

			// Process the message
			_, _ = a.Chat(ctx, input, streamer)
		}
	},
}

func init() {
	rootCmd.AddCommand(chatCmd)
	chatCmd.Flags().StringVarP(&configPath, "config", "c", ".", "Path to config file or directory")
	chatCmd.Flags().BoolVarP(&debugMode, "debug", "d", false, "Log full LLM messages to debug.txt")
	chatCmd.Flags().BoolVarP(&workflowMode, "workflow", "w", false, "Run in workflow mode (non-interactive)")
	chatCmd.Flags().StringVarP(&workflowTask, "task", "t", "", "Task to run in workflow mode (requires --workflow)")
}
