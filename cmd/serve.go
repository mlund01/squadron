package cmd

import (
	"fmt"
	"io"
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
	"squadron/config/vault"
	"squadron/internal/paths"
	"squadron/store"
	"squadron/wsbridge"
)

var (
	serveConfigPath    string
	serveCommandCenter bool
	serveCCPort        int
	serveNoBrowser     bool
	serveAutoInit      bool
	servePassphraseFile string
)

const (
	ccGitHubOwner  = "mlund01"
	ccGitHubRepo   = "squadron-command-center"
	ccKeepAlive    = 30 // seconds
	ccPingInterval = 10 * time.Second
)

func ccBinaryName() string {
	if runtime.GOOS == "windows" {
		return "command-center.exe"
	}
	return "command-center"
}

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
	serveCmd.Flags().BoolVar(&serveAutoInit, "init", false, "Auto-initialize Squadron if not already initialized")
	serveCmd.Flags().StringVar(&servePassphraseFile, "passphrase-file", "", "Path to file containing vault passphrase")
}

func runServe(cmd *cobra.Command, args []string) {
	// Init gate
	config.SetPassphraseFile(servePassphraseFile)
	if err := EnsureInitialized(serveAutoInit, servePassphraseFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Cache passphrase for serve mode (resolved once, used for all var operations)
	if config.IsVaultInitialized() {
		passphrase, err := vault.ResolvePassphrase(servePassphraseFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving vault passphrase: %v\n", err)
			os.Exit(1)
		}
		vault.CachePassphrase(passphrase)
		vault.ZeroBytes(passphrase)
	}

	var ccProc *exec.Cmd
	var ccPort int
	var pingDone chan struct{}
	var localCC bool

	if serveCommandCenter {
		var err error
		ccPort, ccProc, err = launchCommandCenter()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error launching command center: %v\n", err)
			os.Exit(1)
		}
		localCC = true

		pingDone = make(chan struct{})
		go keepAlivePinger(ccPort, pingDone)

		if !serveNoBrowser {
			openBrowser(fmt.Sprintf("http://localhost:%d", ccPort))
		}
	}

	// Try loading config — use partial load so vars/plugins show up even if validation fails
	cfg, cfgErr := config.LoadPartial(serveConfigPath)
	if cfgErr != nil {
		log.Printf("Config not ready: %v", cfgErr)
		log.Println("Waiting for valid configuration (set variables or fix config files)...")
	}

	// When using local command center, override commander config
	if localCC {
		cfg.Commander = localCommanderConfig(cfg, ccPort)
	}

	// Open store — use config storage if available, otherwise default
	storageConfig := cfg.Storage
	if storageConfig == nil {
		storageConfig = config.DefaultStorageConfig(serveConfigPath)
	}
	stores, err := store.NewBundle(storageConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer stores.Close()

	// Create client — cfgErr == nil means config is fully valid
	client := wsbridge.NewClient(cfg, cfgErr == nil, serveConfigPath, stores, Version)

	// When config loads/reloads, update commander override and re-register
	client.OnConfigLoaded = func(newCfg *config.Config) {
		if localCC {
			newCfg.Commander = localCommanderConfig(newCfg, ccPort)
			client.SetConfig(newCfg)
		}
	}

	// Connect immediately — even without valid config, the command center
	// can show vars and config files so the user can fix things from the UI
	if localCC {
		commanderURL := fmt.Sprintf("ws://localhost:%d/ws", ccPort)
		if err := connectWithRetry2(client, commanderURL, true); err != nil {
			log.Printf("Connection failed: %v", err)
		} else {
			fmt.Printf("Connected to command center (instance: %s)\n", client.InstanceID())
		}
	} else if cfg.Commander != nil {
		if err := connectWithRetry2(client, cfg.Commander.URL, cfg.Commander.AutoReconnect); err != nil {
			log.Printf("Connection failed: %v (will retry when config changes)", err)
		} else {
			fmt.Printf("Connected to commander at %s (instance: %s, id: %s)\n",
				cfg.Commander.URL, cfg.Commander.InstanceName, client.InstanceID())
		}
	}

	// If config not fully valid, watch for changes to trigger reload
	if cfgErr != nil {
		go watchForConfigChanges(client, serveConfigPath)
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	go func() {
		for {
			if err := client.Run(); err != nil {
				log.Printf("Connection lost: %v", err)
				cfg := client.GetConfig()
				if cfg != nil && cfg.Commander != nil && cfg.Commander.AutoReconnect {
					log.Println("Attempting to reconnect...")
					if err := connectWithRetry(client, cfg.Commander); err != nil {
						log.Printf("Reconnect failed: %v", err)
						// Don't exit — wait for config changes
						go watchForConfigChanges(client, serveConfigPath)
						return
					}
					continue
				}
			}
			return
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

// localCommanderConfig creates a commander config pointing at the local command center.
func localCommanderConfig(cfg *config.Config, ccPort int) *config.CommanderConfig {
	hostname, _ := os.Hostname()
	instanceName := hostname
	if cfg != nil && cfg.Commander != nil && cfg.Commander.InstanceName != "" {
		instanceName = cfg.Commander.InstanceName
	}
	cc := &config.CommanderConfig{
		URL:               fmt.Sprintf("ws://localhost:%d/ws", ccPort),
		InstanceName:      instanceName,
		AutoReconnect:     true,
		ReconnectInterval: 3,
	}
	cc.Defaults()
	return cc
}

// watchForConfigChanges polls the config path and vars file for changes,
// triggering a config reload when modifications are detected.
func watchForConfigChanges(client *wsbridge.Client, configPath string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastConfigMod := configDirModTime(configPath)
	lastVarsMod := varsFileModTime()

	for range ticker.C {
		if client.IsConnected() && client.HasConfig() {
			// Already connected with valid config — stop polling
			return
		}

		changed := false
		if t := configDirModTime(configPath); t.After(lastConfigMod) {
			lastConfigMod = t
			changed = true
		}
		if t := varsFileModTime(); t.After(lastVarsMod) {
			lastVarsMod = t
			changed = true
		}

		if changed {
			log.Println("Detected changes, reloading config...")
			if err := client.ReloadConfig(); err != nil {
				log.Printf("Config still not ready: %v", err)
			}
		}
	}
}

// configDirModTime returns the latest modification time across all .hcl files in a config path.
func configDirModTime(configPath string) time.Time {
	var latest time.Time
	info, err := os.Stat(configPath)
	if err != nil {
		return latest
	}
	if !info.IsDir() {
		return info.ModTime()
	}
	entries, err := os.ReadDir(configPath)
	if err != nil {
		return latest
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") {
			if fi, err := e.Info(); err == nil && fi.ModTime().After(latest) {
				latest = fi.ModTime()
			}
		}
	}
	return latest
}

// varsFileModTime returns the modification time of the vars file (vault or plaintext).
func varsFileModTime() time.Time {
	// Check vault first
	if vaultPath, err := config.GetVaultFilePath(); err == nil {
		if info, err := os.Stat(vaultPath); err == nil {
			return info.ModTime()
		}
	}
	// Fall back to plaintext
	path, err := config.GetVarsFilePath()
	if err != nil {
		return time.Time{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func connectWithRetry(client *wsbridge.Client, cmdCfg *config.CommanderConfig) error {
	autoReconnect := cmdCfg.AutoReconnect
	return connectWithRetry2(client, cmdCfg.URL, autoReconnect)
}

func connectWithRetry2(client *wsbridge.Client, url string, autoReconnect bool) error {
	maxAttempts := 1
	if autoReconnect {
		maxAttempts = 10
	}
	interval := 3 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := client.ConnectTo(url)
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
		binPath := filepath.Join(baseDir, version, ccBinaryName())
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

	downloadURL, err := findAssetURL(release, ccBinaryName())
	if err != nil {
		return "", err
	}

	archivePath, err := downloadToTemp(downloadURL)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(archivePath)

	extractedPath, err := extractBinaryFromArchive(archivePath, ccBinaryName())
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

	binPath := filepath.Join(versionDir, ccBinaryName())
	if err := moveFile(extractedPath, binPath); err != nil {
		os.Remove(extractedPath)
		return "", err
	}

	// Write current version
	os.WriteFile(currentFile, []byte(version), 0644)

	fmt.Printf("Installed command center %s\n", version)
	return binPath, nil
}

func commandCenterDir() (string, error) {
	sqHome, err := paths.SquadronHome()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(sqHome, "command-center")
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

// moveFile copies src to dst then removes src. Works across filesystem boundaries.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	out.Close()
	os.Remove(src)
	return nil
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
