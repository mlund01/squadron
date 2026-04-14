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
	"syscall"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"squadron/config"
	"squadron/config/vault"
	"squadron/internal/browser"
	"squadron/internal/daemon"
	"squadron/internal/paths"
	squadronmcp "squadron/mcp"
	"squadron/mcphost"
	"squadron/scheduler"
	"squadron/store"
	"squadron/wsbridge"
)

var (
	engageConfigPath string
	engageHeadless   bool
	engageCCPort     int
	engageNoBrowser  bool
	engageAutoInit   bool
	engageForeground bool
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

var engageCmd = &cobra.Command{
	Use:   "engage",
	Short: "Start Squadron and connect to the command center",
	Long: `Start Squadron as a background service with a local command center UI.

By default, Squadron launches the command center web UI, runs in the background,
and installs as a system service (launchd on macOS, systemd on Linux) so it
starts automatically on boot.

Use --headless (-h) to run without the command center UI.
Use --foreground to run in the terminal instead of the background.
Use 'squadron disengage' to stop Squadron and remove the system service.`,
	Run: runEngage,
}

// Hidden backward-compat alias for "serve"
var serveAliasCmd = &cobra.Command{
	Use:    "serve",
	Hidden: true,
	Short:  "Alias for 'engage'",
	Run:    runEngage,
}

func init() {
	rootCmd.AddCommand(engageCmd)
	rootCmd.AddCommand(serveAliasCmd)

	for _, cmd := range []*cobra.Command{engageCmd, serveAliasCmd} {
		cmd.Flags().StringVarP(&engageConfigPath, "config", "c", ".", "Path to config file or directory")
		cmd.Flags().BoolVar(&engageHeadless, "headless", false, "Run without the command center UI")
		cmd.Flags().IntVar(&engageCCPort, "cc-port", 8080, "Port for the command center")
		cmd.Flags().BoolVar(&engageNoBrowser, "no-browser", false, "Don't auto-open browser")
		cmd.Flags().BoolVar(&engageAutoInit, "init", false, "Auto-initialize Squadron if not already initialized")
		cmd.Flags().BoolVar(&engageForeground, "foreground", false, "Run in foreground (default: run as background service)")
	}
}

func runEngage(cmd *cobra.Command, args []string) {
	// Quickstart wizard: only when no .hcl files AND no .squadron/ dir
	hasHCL := hasHCLFiles(engageConfigPath)
	hasSquadron := config.IsVaultInitialized()

	if !hasSquadron && !hasHCL {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintf(os.Stderr, "No configuration found. Run 'squadron quickstart' interactively to set up.\n")
			os.Exit(1)
		}
		dir, err := RunQuickstart(engageConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		engageConfigPath = dir
	} else if !hasSquadron && hasHCL {
		fmt.Println("Initializing Squadron...")
		if err := EnsureInitialized(true); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Init gate (handles case where .squadron exists but vault needs setup)
	if err := EnsureInitialized(engageAutoInit); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Fork to background + install service unless --foreground
	if !engageForeground && term.IsTerminal(int(os.Stdout.Fd())) {
		absConfigPath, err := filepath.Abs(engageConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving config path: %v\n", err)
			os.Exit(1)
		}

		// Build extra flags to pass through
		var extraFlags []string
		if engageHeadless {
			extraFlags = append(extraFlags, "--headless")
		}
		if engageCCPort != 8080 {
			extraFlags = append(extraFlags, "--cc-port", fmt.Sprintf("%d", engageCCPort))
		}
		// Always suppress browser in daemon mode — it's already open from the parent
		extraFlags = append(extraFlags, "--no-browser")

		pid, err := daemon.Fork(absConfigPath, extraFlags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error starting background process: %v\n", err)
			os.Exit(1)
		}

		// Install as system service for boot persistence
		if err := daemon.InstallService(absConfigPath); err != nil {
			// Non-fatal — the process is already running
			log.Printf("Note: could not install system service: %v", err)
		}

		fmt.Printf("Squadron engaged (PID %d). Starts automatically on boot.\n", pid)
		fmt.Println("Use 'squadron disengage' to stop.")

		// Open browser if command center is launching (not headless)
		if !engageHeadless && !engageNoBrowser {
			port := engageCCPort
			// Wait for the command center to become ready
			readyURL := fmt.Sprintf("http://localhost:%d/api/instances", port)
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				resp, err := http.Get(readyURL)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 {
						openBrowser(fmt.Sprintf("http://localhost:%d", port))
						break
					}
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
		return
	}

	// --- Foreground mode (existing serve logic) ---

	// Cache passphrase for serve mode (resolved once, used for all var operations)
	if config.IsVaultInitialized() {
		passphrase, err := vault.ResolvePassphrase("")
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

	// Channel signals when the command center is ready (port, proc, err).
	type ccResult struct {
		port int
		proc *exec.Cmd
		err  error
	}
	ccReady := make(chan ccResult, 1)

	if !engageHeadless {
		go func() {
			port, proc, err := launchCommandCenter()
			ccReady <- ccResult{port, proc, err}
		}()
	} else {
		close(ccReady)
	}

	// Start the MCP host BEFORE the full config load so consumer-side
	// `mcp` blocks can self-reference http://localhost:<port>/mcp without
	// deadlocking. We parse only the mcp_host block here (no expression
	// evaluation), bring up the listening socket, and wire its tool
	// handlers via closures over variables assigned later in startup.
	var (
		sharedClient *wsbridge.Client
		sharedStores *store.Bundle
		mcpServer    *mcphost.Server
	)
	if hostCfg := config.LoadMCPHost(engageConfigPath); hostCfg != nil && hostCfg.Enabled {
		mcpDeps := mcphost.Deps{
			Config: func() *config.Config {
				if sharedClient == nil {
					return nil
				}
				return sharedClient.GetConfig()
			},
			Stores: func() *store.Bundle { return sharedStores },
			RunMission: func(name string, inputs map[string]string) (string, error) {
				if sharedClient == nil {
					return "", fmt.Errorf("squadron is still starting up")
				}
				return sharedClient.RunMissionDirect(name, inputs)
			},
			ReloadConfig: func() error {
				if sharedClient == nil {
					return fmt.Errorf("squadron is still starting up")
				}
				return sharedClient.ReloadConfig()
			},
			Version:    Version,
			ConfigPath: engageConfigPath,
		}
		mcpSrv := mcphost.NewServer(mcpDeps)
		var err error
		mcpServer, err = mcphost.StartStreamableHTTP(mcpSrv, hostCfg.Port, hostCfg.Secret)
		if err != nil {
			log.Printf("Warning: MCP host failed to start: %v", err)
		}
	}

	// Try loading config — use partial load so vars/plugins show up even if validation fails
	cfg, cfgErr := config.LoadPartial(engageConfigPath)
	if cfgErr != nil {
		log.Printf("Config not ready: %v", cfgErr)
		log.Println("Waiting for valid configuration (set variables or fix config files)...")
	}

	// Wait for command center if launching one
	if !engageHeadless {
		result := <-ccReady
		if result.err != nil {
			fmt.Fprintf(os.Stderr, "Error launching command center: %v\n", result.err)
			os.Exit(1)
		}
		ccPort = result.port
		ccProc = result.proc
		localCC = true

		pingDone = make(chan struct{})
		go keepAlivePinger(ccPort, pingDone)

		if !engageNoBrowser {
			openBrowser(fmt.Sprintf("http://localhost:%d", ccPort))
		}
	}

	// When using local command center, override commander config
	if localCC {
		cfg.CommandCenter = localCommandCenterConfig(cfg, ccPort)
	}

	// Open store — use config storage if available, otherwise default
	storageConfig := cfg.Storage
	if storageConfig == nil {
		storageConfig = config.DefaultStorageConfig(engageConfigPath)
	}
	stores, err := store.NewBundle(storageConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer stores.Close()

	// Create client — cfgErr == nil means config is fully valid
	cfgErrMsg := ""
	if cfgErr != nil {
		cfgErrMsg = cfgErr.Error()
	}
	client := wsbridge.NewClient(cfg, cfgErr == nil, cfgErrMsg, engageConfigPath, stores, Version)

	// Create scheduler — owns cron timers and concurrency tracking
	sched := scheduler.New(client.RunScheduledMission)
	client.SetConcurrencyTracker(sched)
	if cfgErr == nil && cfg != nil {
		sched.UpdateConfig(cfg)
	}

	// When config loads/reloads, update commander override and re-register
	client.OnConfigLoaded = func(newCfg *config.Config) {
		if localCC {
			newCfg.CommandCenter = localCommandCenterConfig(newCfg, ccPort)
			client.SetConfig(newCfg)
		}
		sched.UpdateConfig(newCfg)
	}

	// Wire deferred deps now that client + stores exist. The MCP host is
	// already running (started above, before the full config load) so any
	// in-flight tool calls coming through it will start succeeding now.
	sharedClient = client
	sharedStores = stores

	// Connect — even without valid config, the command center
	// can show vars and config files so the user can fix things from the UI
	if localCC {
		commanderURL := fmt.Sprintf("ws://localhost:%d/ws", ccPort)
		if err := connectWithRetry2(client, commanderURL, true); err != nil {
			log.Printf("Connection failed: %v", err)
		} else {
			fmt.Printf("Squadron ready — http://localhost:%d\n", ccPort)
		}
	} else if cfg.CommandCenter != nil {
		if err := connectWithRetry2(client, cfg.CommandCenter.URL, cfg.CommandCenter.AutoReconnect); err != nil {
			log.Printf("Connection failed: %v (will retry when config changes)", err)
		} else {
			fmt.Printf("Squadron ready — connected to %s (instance: %s)\n",
				cfg.CommandCenter.URL, cfg.CommandCenter.InstanceName)
		}
	}

	// Auto-resume any missions that were running when the process last died
	if cfgErr == nil {
		client.ResumeOrphanedMissions()
	}

	// If config not fully valid, watch for changes to trigger reload
	if cfgErr != nil {
		go watchForConfigChanges(client, engageConfigPath)
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		for {
			if err := client.Run(); err != nil {
				if !client.IsConnected() {
					return // shutting down
				}
				log.Printf("Connection lost: %v", err)
				cfg := client.GetConfig()
				if cfg != nil && cfg.CommandCenter != nil && cfg.CommandCenter.AutoReconnect {
					log.Println("Attempting to reconnect...")
					if err := connectWithRetry(client, cfg.CommandCenter); err != nil {
						log.Printf("Reconnect failed: %v", err)
						// Don't exit — wait for config changes
						go watchForConfigChanges(client, engageConfigPath)
						return
					}
					continue
				}
			}
			return
		}
	}()

	<-stop
	fmt.Println("Shutting down...")

	// Close the wsbridge client first so the reconnect goroutine won't
	// try to reconnect when the command center process exits.
	client.Close()

	squadronmcp.CloseAll() // close MCP clients before stopping the host
	mcpServer.Shutdown()
	sched.Stop()

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

// hasHCLFiles checks if any .hcl files exist at the given path.
func hasHCLFiles(configPath string) bool {
	info, err := os.Stat(configPath)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return strings.HasSuffix(configPath, ".hcl")
	}
	entries, err := os.ReadDir(configPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") {
			return true
		}
	}
	return false
}

// localCommandCenterConfig creates a command center config pointing at the local command center.
func localCommandCenterConfig(cfg *config.Config, ccPort int) *config.CommandCenterConfig {
	hostname, _ := os.Hostname()
	instanceName := hostname
	if cfg != nil && cfg.CommandCenter != nil && cfg.CommandCenter.InstanceName != "" {
		instanceName = cfg.CommandCenter.InstanceName
	}
	cc := &config.CommandCenterConfig{
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

func connectWithRetry(client *wsbridge.Client, cmdCfg *config.CommandCenterConfig) error {
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

	port, err := findFreePort(engageCCPort)
	if err != nil {
		return 0, nil, fmt.Errorf("could not find free port: %w", err)
	}

	proc := exec.Command(binaryPath,
		"-addr", fmt.Sprintf(":%d", port),
		"-keep-alive", fmt.Sprintf("%d", ccKeepAlive),
	)

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

	fmt.Printf("Downloaded command center %s\n", version)
	return binPath, nil
}

func commandCenterDir() (string, error) {
	sqHome, err := paths.SquadronHome()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(sqHome, "command-center", paths.PlatformDir())
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
	browser.Open(url)
}
