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
	"syscall"
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
	engageCC         bool
	engageCCPort     int
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
	Long: `Start Squadron as a background service.

By default, Squadron runs headless. Pass --cc to launch the local command
center web UI. If your config declares a command_center block, Squadron
connects outbound to that instead (and --cc becomes an error).

Squadron runs in the background and installs as a system service (launchd
on macOS, systemd on Linux) so it starts automatically on boot.

Use --foreground to run in the terminal instead of the background.
Use 'squadron disengage' to stop Squadron and remove the system service.`,
	Run: runEngage,
}

func init() {
	rootCmd.AddCommand(engageCmd)

	engageCmd.Flags().StringVarP(&engageConfigPath, "config", "c", ".", "Path to config file or directory")
	engageCmd.Flags().BoolVar(&engageCC, "cc", false, "Launch the local command center UI")
	engageCmd.Flags().IntVar(&engageCCPort, "cc-port", 8080, "Port for the command center")
	engageCmd.Flags().BoolVar(&engageAutoInit, "init", false, "Auto-initialize Squadron if not already initialized")
	engageCmd.Flags().BoolVar(&engageForeground, "foreground", false, "Run in foreground (default: run as background service)")
}

func runEngage(cmd *cobra.Command, args []string) {
	// In a container, the process IS the daemon — don't double-fork.
	if isContainer() {
		engageForeground = true
	}

	if warning, err := validateConfigDir(engageConfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	} else if warning != "" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "Warning: %s\n\n", warning)
		if !promptYesNo("Continue anyway?") {
			return
		}
	}

	hasHCL := hasHCLFiles(engageConfigPath)
	hasSquadron := config.IsVaultInitialized()

	switch {
	case !hasSquadron && !hasHCL && term.IsTerminal(int(os.Stdin.Fd())):
		// Fresh install on an interactive terminal — run the full wizard.
		dir, err := RunQuickstart(engageConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		engageConfigPath = dir
	case !hasSquadron:
		// No interactive terminal, or HCL is already present — just init the
		// vault. Any remaining setup happens through the command center UI.
		if err := EnsureInitialized(true); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	if err := EnsureInitialized(engageAutoInit); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// --cc and a command_center block are two different sources of truth.
	hasRemoteCC := configHasCommandCenter(engageConfigPath)
	if engageCC && hasRemoteCC {
		fmt.Fprintln(os.Stderr, "Error: --cc is incompatible with a command_center block in the config. Remove one or the other.")
		os.Exit(1)
	}
	if !engageCC && !hasRemoteCC {
		fmt.Fprintln(os.Stderr, "Warning: running in headless mode without a command center.")
		fmt.Fprintln(os.Stderr, "  Squadron will run schedules and webhooks but has no UI and no remote control.")
		fmt.Fprintln(os.Stderr, "  Pass --cc to launch the local UI, or add a command_center block to your config.")
		fmt.Fprintln(os.Stderr)
	}
	launchLocalCC := engageCC

	if !engageForeground && term.IsTerminal(int(os.Stdout.Fd())) {
		absConfigPath, err := filepath.Abs(engageConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving config path: %v\n", err)
			os.Exit(1)
		}

		var extraFlags []string
		if engageCC {
			extraFlags = append(extraFlags, "--cc")
		}
		if engageCCPort != 8080 {
			extraFlags = append(extraFlags, "--cc-port", fmt.Sprintf("%d", engageCCPort))
		}

		daemon.ClearReady(absConfigPath)
		sp := startSpinner("Starting Squadron")

		pid, err := daemon.Fork(absConfigPath, extraFlags)
		if err != nil {
			sp.Stop()
			fmt.Fprintf(os.Stderr, "Error starting background process: %v\n", err)
			os.Exit(1)
		}

		ready := daemon.WaitReady(absConfigPath, 60*time.Second, 2*time.Second)
		sp.Stop()

		if !ready.OK {
			daemon.CleanupFailedFork(absConfigPath)
			fmt.Fprintf(os.Stderr, "Error: %s\n", ready.Error)
			os.Exit(1)
		}

		// Install as system service for boot persistence. Non-fatal: the process
		// is already running, we just won't auto-start next boot.
		if err := daemon.InstallService(absConfigPath); err != nil {
			log.Printf("Note: could not install system service: %v", err)
		}

		fmt.Printf("Squadron engaged (PID %d). Starts automatically on boot.\n", pid)
		fmt.Println("Use 'squadron disengage' to stop.")

		// Open the port the child actually bound — may differ from --cc-port if taken.
		if launchLocalCC && ready.CCPort > 0 {
			openBrowser(fmt.Sprintf("http://localhost:%d", ready.CCPort))
		}
		return
	}

	// --- Foreground mode ---

	daemon.ClearReady(engageConfigPath)

	// Resolve the vault passphrase once at startup and keep it in memory for
	// all later var operations on this process.
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

	type ccResult struct {
		port int
		proc *exec.Cmd
		err  error
	}
	ccReady := make(chan ccResult, 1)

	if launchLocalCC {
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

	// Best-effort load — missing vars or partial validation errors don't stop
	// startup; the command center UI lets the user fix them in place.
	cfg, cfgErr := config.LoadPartial(engageConfigPath)
	if cfgErr != nil {
		log.Println("No valid configuration yet. Use the command center UI to create or edit HCL files.")
	}

	if launchLocalCC {
		result := <-ccReady
		if result.err != nil {
			daemon.SignalFailed(engageConfigPath, fmt.Errorf("launching command center: %w", result.err))
			fmt.Fprintf(os.Stderr, "Error launching command center: %v\n", result.err)
			os.Exit(1)
		}
		ccPort = result.port
		ccProc = result.proc
		localCC = true

		pingDone = make(chan struct{})
		go keepAlivePinger(ccPort, pingDone)
	}

	daemon.SignalReady(engageConfigPath, ccPort)

	if localCC {
		cfg.CommandCenter = localCommandCenterConfig(cfg, ccPort)
	}

	storageConfig := cfg.Storage
	if storageConfig == nil {
		storageConfig = config.DefaultStorageConfig(engageConfigPath)
	}
	stores, err := store.NewBundle(storageConfig)
	if err != nil {
		daemon.SignalFailed(engageConfigPath, fmt.Errorf("could not open storage: %w", err))
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer stores.Close()

	cfgErrMsg := ""
	if cfgErr != nil {
		cfgErrMsg = cfgErr.Error()
	}
	client := wsbridge.NewClient(cfg, cfgErr == nil, cfgErrMsg, engageConfigPath, stores, Version)

	sched := scheduler.New(client.RunScheduledMission)
	client.SetConcurrencyTracker(sched)
	if cfgErr == nil {
		sched.UpdateConfig(cfg)
	}

	client.OnConfigLoaded = func(newCfg *config.Config) {
		if localCC {
			newCfg.CommandCenter = localCommandCenterConfig(newCfg, ccPort)
			client.SetConfig(newCfg)
		}
		sched.UpdateConfig(newCfg)
		daemon.SignalReady(engageConfigPath, ccPort)
	}

	// Wire deferred deps now that client + stores exist. The MCP host is
	// already running (started above, before the full config load) so any
	// in-flight tool calls coming through it will start succeeding now.
	sharedClient = client
	sharedStores = stores

	// Even without valid config we still try to connect — the command center
	// can show vars and config files so the user can fix things from the UI.
	if localCC {
		commanderURL := fmt.Sprintf("ws://localhost:%d/ws", ccPort)
		if err := connectWithRetry(client, commanderURL, true); err != nil {
			log.Printf("Connection failed: %v", err)
		} else {
			fmt.Printf("Squadron ready — http://localhost:%d\n", ccPort)
		}
	} else if cfg.CommandCenter != nil {
		if err := connectWithRetry(client, cfg.CommandCenter.URL, cfg.CommandCenter.AutoReconnect); err != nil {
			log.Printf("Connection failed: %v (will retry when config changes)", err)
		} else {
			fmt.Printf("Squadron ready — connected to %s (instance: %s)\n",
				cfg.CommandCenter.URL, cfg.CommandCenter.InstanceName)
		}
	}

	if cfgErr == nil {
		client.ResumeOrphanedMissions()
	} else {
		// Watch for files to appear/change so we can retry the load.
		go watchForConfigChanges(client, engageConfigPath)
	}

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
					if err := connectWithRetry(client, cfg.CommandCenter.URL, cfg.CommandCenter.AutoReconnect); err != nil {
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
	daemon.ClearReady(engageConfigPath)

	// Close the wsbridge client first so the reconnect loop won't chase the
	// command-center process as it shuts down.
	client.Close()

	squadronmcp.CloseAll() // close MCP clients before stopping the host
	if mcpServer != nil {
		mcpServer.Shutdown()
	}
	sched.Stop()

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

// isContainer reports whether we're running inside a container. The official
// image sets SQUADRON_CONTAINER=1 in its ENV.
func isContainer() bool {
	return os.Getenv("SQUADRON_CONTAINER") == "1"
}

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

// validateConfigDir checks the config directory for common mistakes.
// Returns an error for hard failures, a warning string for soft issues.
func validateConfigDir(configPath string) (warning string, err error) {
	absPath, pathErr := filepath.Abs(configPath)
	if pathErr != nil {
		return "", pathErr
	}
	dir := absPath
	if info, statErr := os.Stat(absPath); statErr == nil && !info.IsDir() {
		dir = filepath.Dir(absPath)
	}

	// Error: nested .squadron directories — walk subdirectories (max 3 levels)
	// to detect projects inside projects.
	filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		// Skip the root .squadron itself
		if path == filepath.Join(dir, ".squadron") {
			return filepath.SkipDir
		}
		// Skip deep traversal
		rel, _ := filepath.Rel(dir, path)
		if strings.Count(rel, string(filepath.Separator)) > 3 {
			return filepath.SkipDir
		}
		if d.Name() == ".squadron" {
			err = fmt.Errorf("nested .squadron directory found at %s — squadron projects cannot be nested", path)
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	// Warning: directory is too high-level (home dir, filesystem root, etc.)
	home, _ := os.UserHomeDir()
	warnDirs := []string{"/", home}
	// Add common root dirs
	for _, d := range []string{"/tmp", "/var", "/etc", "/usr"} {
		warnDirs = append(warnDirs, d)
	}
	// Add home parent dirs (e.g. /Users, /Users/maxlund, /home, /home/maxlund)
	if home != "" {
		warnDirs = append(warnDirs, filepath.Dir(home))
	}

	for _, w := range warnDirs {
		if dir == w {
			warning = fmt.Sprintf("You are running in %s — this is a high-level directory.\n"+
				"  Consider creating a project directory first, e.g.:\n"+
				"    mkdir my-project && cd my-project", dir)
			break
		}
	}

	return warning, nil
}

// configHasCommandCenter scans HCL files for an uncommented command_center block.
func configHasCommandCenter(configPath string) bool {
	var files []string
	info, err := os.Stat(configPath)
	if err != nil {
		return false
	}
	if info.IsDir() {
		entries, _ := os.ReadDir(configPath)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") {
				files = append(files, filepath.Join(configPath, e.Name()))
			}
		}
	} else {
		files = []string{configPath}
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.HasPrefix(trimmed, "command_center") {
				return true
			}
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

func connectWithRetry(client *wsbridge.Client, url string, autoReconnect bool) error {
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
	if err := os.WriteFile(currentFile, []byte(version), 0644); err != nil {
		return "", fmt.Errorf("writing current version marker: %w", err)
	}

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
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Remove(src)
}

func openBrowser(url string) {
	browser.Open(url)
}
