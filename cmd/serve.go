package cmd

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"squadron/config"
	"squadron/store"
	"squadron/wsbridge"
)

var (
	serveConfigPath    string
	serveCommandCenter bool
	serveCCPort        int
	serveNoBrowser     bool
)

const (
	ccGitHubOwner = "mlund01"
	ccGitHubRepo  = "squadron-command-center"
	ccBinaryName  = "command-center"
	ccKeepAlive   = 30 // seconds
	ccPingInterval = 10 * time.Second
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Connect to a commander server and serve missions",
	Long: `Start a long-running process that connects to a commander server via WebSocket.
The instance registers with commander, allowing remote config inspection,
mission execution, and history queries.

Requires a "commander" block in the config with url and instance_name,
or use --command-center (-w) to automatically launch a local command center.`,
	Run: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVarP(&serveConfigPath, "config", "c", ".", "Path to config file or directory")
	serveCmd.Flags().BoolVarP(&serveCommandCenter, "command-center", "w", false, "Launch a local command center instance")
	serveCmd.Flags().IntVar(&serveCCPort, "cc-port", 8080, "Port for the command center")
	serveCmd.Flags().BoolVar(&serveNoBrowser, "no-browser", false, "Don't auto-open browser")
}

func runServe(cmd *cobra.Command, args []string) {
	cfg, err := config.LoadAndValidate(serveConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	var ccProc *exec.Cmd
	var ccPort int
	var pingDone chan struct{}

	if serveCommandCenter {
		// Launch a local command center
		ccPort, ccProc, err = launchCommandCenter()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error launching command center: %v\n", err)
			os.Exit(1)
		}

		// Override commander config to point at local instance
		hostname, _ := os.Hostname()
		instanceName := hostname
		if cfg.Commander != nil && cfg.Commander.InstanceName != "" {
			instanceName = cfg.Commander.InstanceName
		}
		cfg.Commander = &config.CommanderConfig{
			URL:               fmt.Sprintf("ws://localhost:%d/ws", ccPort),
			InstanceName:      instanceName,
			AutoReconnect:     true,
			ReconnectInterval: 3,
		}
		cfg.Commander.Defaults()

		// Start keep-alive pinger
		pingDone = make(chan struct{})
		go keepAlivePinger(ccPort, pingDone)

		if !serveNoBrowser {
			openBrowser(fmt.Sprintf("http://localhost:%d", ccPort))
		}
	}

	if cfg.Commander == nil {
		fmt.Fprintln(os.Stderr, "Error: no commander block in config. Add a commander block with url and instance_name, or use --command-center (-w).")
		os.Exit(1)
	}

	// Open store
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
	signal.Notify(stop, os.Interrupt)

	go func() {
		if err := client.Run(); err != nil {
			log.Printf("Connection lost: %v", err)
			if cfg.Commander.AutoReconnect {
				log.Println("Attempting to reconnect...")
				if err := connectWithRetry(client, cfg.Commander); err != nil {
					log.Printf("Reconnect failed: %v", err)
					stop <- os.Interrupt
				}
			} else {
				stop <- os.Interrupt
			}
		}
	}()

	<-stop
	fmt.Println("\nShutting down...")
	client.Close()

	// Clean up command center
	if pingDone != nil {
		close(pingDone)
	}
	if ccProc != nil && ccProc.Process != nil {
		ccProc.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- ccProc.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			ccProc.Process.Kill()
		}
	}
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

// launchCommandCenter ensures the command center binary is installed,
// finds a free port, launches the process, and waits for it to be ready.
func launchCommandCenter() (int, *exec.Cmd, error) {
	binaryPath, err := ensureCommandCenter()
	if err != nil {
		return 0, nil, err
	}

	port, err := findFreePort(serveCCPort)
	if err != nil {
		return 0, nil, fmt.Errorf("could not find free port: %w", err)
	}

	proc := exec.Command(binaryPath,
		"-addr", fmt.Sprintf(":%d", port),
		"-keep-alive", fmt.Sprintf("%d", ccKeepAlive),
	)
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr

	if err := proc.Start(); err != nil {
		return 0, nil, fmt.Errorf("failed to start command center: %w", err)
	}

	// Wait for readiness
	readyURL := fmt.Sprintf("http://localhost:%d/api/instances", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(readyURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				fmt.Printf("Command center running at http://localhost:%d\n", port)
				return port, proc, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Timed out — kill and report
	proc.Process.Kill()
	return 0, nil, fmt.Errorf("command center did not become ready within 5 seconds")
}

// ensureCommandCenter checks for an installed binary, downloading if necessary.
func ensureCommandCenter() (string, error) {
	baseDir, err := commandCenterDir()
	if err != nil {
		return "", err
	}

	// Check for existing installation
	currentFile := filepath.Join(baseDir, "current")
	if data, err := os.ReadFile(currentFile); err == nil {
		version := strings.TrimSpace(string(data))
		binPath := filepath.Join(baseDir, version, ccBinaryName)
		if _, err := os.Stat(binPath); err == nil {
			return binPath, nil
		}
	}

	// Download latest
	fmt.Println("Downloading command center...")
	release, err := fetchLatestRelease(ccGitHubOwner, ccGitHubRepo)
	if err != nil {
		return "", fmt.Errorf("failed to fetch release: %w", err)
	}

	downloadURL, err := findAssetURL(release, ccBinaryName)
	if err != nil {
		return "", err
	}

	archivePath, err := downloadToTemp(downloadURL)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(archivePath)

	extractedPath, err := extractBinaryFromArchive(archivePath, ccBinaryName)
	if err != nil {
		return "", fmt.Errorf("extraction failed: %w", err)
	}

	// Install to versioned directory
	version := release.TagName
	versionDir := filepath.Join(baseDir, version)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		os.Remove(extractedPath)
		return "", err
	}

	binPath := filepath.Join(versionDir, ccBinaryName)
	if err := os.Rename(extractedPath, binPath); err != nil {
		os.Remove(extractedPath)
		return "", err
	}

	// Write current version
	os.WriteFile(currentFile, []byte(version), 0644)

	fmt.Printf("Installed command center %s\n", version)
	return binPath, nil
}

func commandCenterDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".squadron", "command-center")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func findFreePort(preferred int) (int, error) {
	for port := preferred; port <= preferred+10; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found in range %d-%d", preferred, preferred+10)
}

func keepAlivePinger(port int, done chan struct{}) {
	url := fmt.Sprintf("http://localhost:%d/api/keep-alive", port)
	ticker := time.NewTicker(ccPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			http.Post(url, "", nil)
		}
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	cmd.Start()
}
