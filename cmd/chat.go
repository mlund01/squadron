package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"squadron/agent"
	"squadron/config"
	"squadron/internal/paths"
	"squadron/mission"
	"squadron/streamers/cli"

	"github.com/spf13/cobra"
)

var configPath string
var debugMode bool
var missionMode bool
var missionTask string
var chatAutoInit bool

var chatCmd = &cobra.Command{
	Use:   "chat [agent_name]",
	Short: "Chat with a given agent",
	Long:  `Start an interactive chat session with the specified agent.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := applyHome(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if err := EnsureInitialized(chatAutoInit); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		agentName := args[0]
		ctx := context.Background()

		// Build agent options
		opts := agent.Options{
			ConfigPath: configPath,
			AgentName:  agentName,
		}
		cfg, err := config.LoadAndValidate(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		opts.Config = cfg
		for i := range cfg.Agents {
			definition := &cfg.Agents[i]
			if definition.Name != agentName || definition.Memory == nil || definition.Memory.Mode == "disabled" {
				continue
			}
			currentUser, err := user.Current()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error identifying local user: %v\n", err)
				os.Exit(1)
			}
			home, err := paths.SquadronHome()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error locating Squadron state: %v\n", err)
				os.Exit(1)
			}
			key := "persistent"
			if definition.Memory.Mode == "session" {
				bytes := make([]byte, 16)
				if _, err := rand.Read(bytes); err != nil {
					fmt.Fprintf(os.Stderr, "Error creating session identity: %v\n", err)
					os.Exit(1)
				}
				key = hex.EncodeToString(bytes)
			}
			opts.MemoryStore, err = agent.NewManagedMemoryStore(home, definition.Memory, "cli:"+currentUser.Uid, agentName, key)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error opening agent memory: %v\n", err)
				os.Exit(1)
			}
			break
		}

		if missionMode {
			mode := config.ModeMission
			opts.Mode = &mode
		}

		// Create debug logger if debug mode is enabled
		var debugDir string
		if debugMode {
			debugDir = filepath.Join("debug", fmt.Sprintf("chat_%s_%s", agentName, time.Now().Format("20060102_150405")))
		}
		debugLogger, err := mission.NewDebugLogger(debugDir)
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

		// Mission mode: non-interactive, run task to completion
		if missionMode && missionTask != "" {
			fmt.Printf("\n📋 Running mission task: %s\n\n", missionTask)
			_, _ = a.Chat(ctx, missionTask, streamer)
			fmt.Println("\n✅ Mission complete")
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
	chatCmd.Flags().BoolVarP(&missionMode, "mission", "w", false, "Run in mission mode (non-interactive)")
	chatCmd.Flags().StringVarP(&missionTask, "task", "t", "", "Task to run in mission mode (requires --mission)")
	chatCmd.Flags().BoolVar(&chatAutoInit, "init", false, "Auto-initialize Squadron if not already initialized")
}
