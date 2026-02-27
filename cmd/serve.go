package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"squadron/config"
	"squadron/store"
	"squadron/wsbridge"
)

var serveConfigPath string

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Connect to a commander server and serve missions",
	Long: `Start a long-running process that connects to a commander server via WebSocket.
The instance registers with commander, allowing remote config inspection,
mission execution, and history queries.

Requires a "commander" block in the config with url and instance_name.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadAndValidate(serveConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}

		if cfg.Commander == nil {
			fmt.Fprintln(os.Stderr, "Error: no commander block in config. Add a commander block with url and instance_name.")
			os.Exit(1)
		}

		// Open store using the same config as the Runner, so history queries see mission data
		stores, err := store.NewBundle(cfg.Storage)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
			os.Exit(1)
		}
		defer stores.Close()

		client := wsbridge.NewClient(cfg, serveConfigPath, stores, Version)

		// Connect with retry
		if err := connectWithRetry(client, cfg.Commander); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Connected to commander at %s (instance: %s, id: %s)\n",
			cfg.Commander.URL, cfg.Commander.InstanceName, client.InstanceID())

		// Graceful shutdown
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			if err := client.Run(); err != nil {
				log.Printf("Connection lost: %v", err)
				if cfg.Commander.AutoReconnect {
					log.Println("Attempting to reconnect...")
					if err := connectWithRetry(client, cfg.Commander); err != nil {
						log.Printf("Reconnect failed: %v", err)
						stop <- syscall.SIGTERM
					}
				} else {
					stop <- syscall.SIGTERM
				}
			}
		}()

		<-stop
		fmt.Println("\nShutting down...")
		client.Close()
	},
}

func connectWithRetry(client *wsbridge.Client, cmdCfg *config.CommanderConfig) error {
	maxAttempts := 1
	if cmdCfg.AutoReconnect {
		maxAttempts = 10
	}
	interval := time.Duration(cmdCfg.ReconnectInterval) * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := client.Connect()
		if err == nil {
			return nil
		}
		if attempt == maxAttempts {
			return fmt.Errorf("failed to connect after %d attempts: %w", maxAttempts, err)
		}
		log.Printf("Connection attempt %d/%d failed: %v. Retrying in %v...", attempt, maxAttempts, err, interval)
		time.Sleep(interval)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVarP(&serveConfigPath, "config", "c", ".", "Path to config file or directory")
}
